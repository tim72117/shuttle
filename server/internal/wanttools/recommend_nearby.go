package wanttools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tim72117/tripace/internal/geo"
	"github.com/tim72117/want/types"
)

// RecommendNearbyDeclaration 是給 LLM 看的工具宣告。
// 依地點(城市/地標名稱)找附近高評分景點,供規劃行程時參考、寫入條目。
// 只負責回傳候選清單,不決定怎麼排進行程——排程判斷交給 agent。
var RecommendNearbyDeclaration = types.ToolDeclaration{
	Name: "recommend_nearby",
	Description: "依地點名稱查詢附近的推薦景點/餐廳/住宿等,依評分與評論數排序。" +
		"規劃行程需要湊景點時呼叫。" +
		"place 應包含城市名以提高定位準確度(如「京都清水寺」而非僅「清水寺」)。" +
		"回傳候選清單(名稱、地址、評分、評論數),由你判斷哪些適合寫入行程。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"place": map[string]interface{}{
				"type":        "STRING",
				"description": "查詢中心點的地點名稱,應包含城市或地區名稱,例如「京都車站」、「宮古島市」。",
			},
			"category": map[string]interface{}{
				"type": "STRING",
				"description": "想找的類型,填 Google Places API 官方類別 key(英文,小寫底線),從下面清單挑" +
					"最貼切使用者描述的一個(例如使用者說「拉麵」就填 ramen_restaurant 而非泛用的 " +
					"restaurant,說「日式旅館」就填 japanese_inn 而非泛用的 lodging;找不到夠精確的" +
					"對應詞才退回較泛用的類別如 restaurant/tourist_attraction)。留空表示不限類型," +
					"回傳綜合推薦。可用類別:\n" + placeCategoryList,
			},
			"radius_meters": map[string]interface{}{
				"type":        "INTEGER",
				"description": "搜尋半徑(公尺),預設 1500,最大 50000。步行範圍建議 1000~2000,市區範圍可用 5000~10000。",
			},
		},
		"required": []string{"place"},
	},
}

// placeCategoryList 是 Google Places API 官方類別 key 的清單(旅遊/景點相關子集,
// 已排除律師/水電工/汽車經銷商等服務型地點),直接嵌進 category 參數的 description
// 給 LLM 看,由 LLM 自己從使用者的自然語言描述對應到最精確的官方 key——不再維護
// 「中文詞→類別」的映射表(覆蓋率永遠有限,LLM 的語言理解本身就能做這個對應)。
// 完整清單: https://developers.google.com/maps/documentation/places/web-service/place-types
const placeCategoryList = `景點/文化: tourist_attraction, museum, art_gallery, art_museum, historical_place, historical_landmark, cultural_landmark, monument, castle, sculpture, observation_deck, botanical_garden, zoo, aquarium, amusement_park, water_park, national_park, state_park, city_park, garden, hiking_area, wildlife_park, planetarium, opera_house, concert_hall, performing_arts_theater, movie_theater, amphitheatre, plaza, marina, visitor_center
餐飲: restaurant, cafe, bar, bakery, buffet_restaurant, fine_dining_restaurant, fast_food_restaurant, food_court, sushi_restaurant, ramen_restaurant, japanese_restaurant, japanese_izakaya_restaurant, chinese_restaurant, korean_restaurant, taiwanese_restaurant, thai_restaurant, vietnamese_restaurant, italian_restaurant, french_restaurant, seafood_restaurant, steak_house, hamburger_restaurant, pizza_restaurant, korean_barbecue_restaurant, hot_pot_restaurant, dessert_shop, ice_cream_shop, coffee_shop, tea_house, brewery, wine_bar, cocktail_bar
住宿: lodging, hotel, resort_hotel, hostel, motel, inn, bed_and_breakfast, japanese_inn, guest_house, campground, rv_park
購物: shopping_mall, supermarket, convenience_store, department_store, market, farmers_market, book_store, gift_shop, jewelry_store, clothing_store
休閒娛樂: spa, public_bath, sauna, gym, fitness_center, golf_course, ski_resort, bowling_alley, karaoke, night_club, casino, movie_theater, amusement_center, water_park, swimming_pool
交通地標: train_station, subway_station, bus_station, airport, ferry_terminal`

type RecommendNearbyTool struct {
	types.BaseToolConfig
}

func (t *RecommendNearbyTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	if args.GetString("place") == "" {
		return fmt.Errorf("place is required")
	}
	return nil
}

func (t *RecommendNearbyTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	place := args.GetString("place")
	category := strings.TrimSpace(args.GetString("category"))
	radius := float64(args.GetInt("radius_meters"))

	apiKey := os.Getenv("GOOGLE_PLACES_API_KEY")
	client := geo.New(apiKey)

	gctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// 先把地點名稱解析成座標,再以座標查附近景點(Nearby Search 需要座標中心點)。
	center, err := client.Search(gctx, place, &geo.SearchOptions{MaxResults: 1})
	if err != nil {
		return nil, fmt.Errorf("定位「%s」失敗: %w", place, err)
	}
	origin := center[0]

	// category 現在就是 LLM 依 placeCategoryList 選出的官方 Places API 類別 key,
	// 直接使用,不再需要中文→英文的查表轉換。
	var includedTypes []string
	if category != "" {
		includedTypes = []string{category}
	}

	nearby, err := client.SearchNearby(gctx, origin.Lat, origin.Lng, &geo.NearbyOptions{
		RadiusMeters:  radius,
		IncludedTypes: includedTypes,
		MaxResults:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("查詢「%s」附近景點失敗: %w", place, err)
	}
	// 不額外排序:field mask 只取 Essentials 級欄位(呼叫成本最低),不含評分,
	// 直接沿用 Places API Nearby Search 本身的相關性排序。

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("「%s」附近推薦(以 %s 為中心):\n", place, origin.Name))
	for _, p := range nearby {
		sb.WriteString(fmt.Sprintf("・%s%s - %s\n", p.Name, formatType(p.PrimaryType), p.Address))
	}
	summary := strings.TrimRight(sb.String(), "\n")

	results := make([]map[string]interface{}, 0, len(nearby))
	places := make([]RecommendedPlace, 0, len(nearby))
	for _, p := range nearby {
		results = append(results, map[string]interface{}{
			"name":        p.Name,
			"address":     p.Address,
			"lat":         p.Lat,
			"lng":         p.Lng,
			"primaryType": p.PrimaryType,
		})
		places = append(places, RecommendedPlace{
			Name: p.Name, Address: p.Address, Lat: p.Lat, Lng: p.Lng, PrimaryType: p.PrimaryType,
		})
	}

	ctx.EmitToolResult(map[string]interface{}{
		"summary": summary,
		"origin":  map[string]interface{}{"name": origin.Name, "lat": origin.Lat, "lng": origin.Lng},
		"results": results,
	})
	// recommend_nearby 與 present_entries 同屬 sync 型工具,都在同一次
	// want_analyzer.Assist() 推論流程內執行(見 sink.go 的 RecordLock/RecordUnlock
	// 生命週期)。故比照 present_entries 的 addPresented,把候選景點累積進本次
	// 流程的結果,由 Assist() 結束後一併轉成 AssistResult.RecommendedPlaces
	// 回傳給前端,掛在觸發它的那則訊息底下顯示——不再用異步 WS 廣播
	// (原本的 NotifyRecommendedPlaces 只帶 channelID,無法對應到是哪一則訊息
	// 觸發的,同一輪對話連續呼叫兩次還會互相覆蓋)。
	addRecommendedPlaces(places)
	return []types.ResultContentBlock{types.TextBlock(summary)}, nil
}

func formatType(primaryType string) string {
	if primaryType == "" {
		return ""
	}
	return " [" + primaryType + "]"
}

func (t *RecommendNearbyTool) RenderToolUse(args types.ToolArguments) string {
	place := args.GetString("place")
	// category 現在是 Places API 官方英文 key(如 ramen_restaurant),不適合直接
	// 拼進中文句子顯示,提示文字統一用「推薦景點」帶過即可。
	return fmt.Sprintf("正在搜尋「%s」附近的推薦景點...", place)
}

func (t *RecommendNearbyTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("推薦景點查詢失敗: %v", err)
}

func (t *RecommendNearbyTool) RenderToolResult(data map[string]interface{}) string {
	return "已找到推薦景點"
}

func init() {
	types.RegisterTool(RecommendNearbyDeclaration, func() types.ToolInterface {
		return &RecommendNearbyTool{}
	})
}
