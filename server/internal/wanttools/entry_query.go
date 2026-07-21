package wanttools

// entry_query 改造說明(第二階段重構):這個工具原本查完直接把條目內容組成
// 文字,透過 ctx.EmitToolResult 與回傳值送回給 LLM 自己讀取判斷(見 present_entries
// 對應的舊文字組裝邏輯)。改造後,查詢邏輯本身不變(一樣呼叫
// entryStore.ListEntriesByRange),但查到的結果不再回給 LLM 讀——改成轉換成
// 前端 TripEntry 格式(TripEntryPayload,見 sink.go),透過 NotifyEntriesLoaded
// 廣播 entries_loaded 事件推給前端,由前端合併進旅程清單表格供使用者查看/編輯。
// Call 回傳給 LLM 的內容只剩一句簡短確認文字(如「已載入 3 筆到前端表格」),
// 不含任何條目的具體內容——LLM 只需要知道查詢範圍有沒有觸發成功,不需要(也
// 不會)拿到資料本身去做任何判斷或回答,這是這次設計的核心。

import (
	"fmt"
	"time"

	"github.com/tim72117/tripace/internal/model"
	"github.com/tim72117/tripace/internal/store"
	"github.com/tim72117/want/types"
)

// entryStore 是工具共用的 store 實例(server 啟動時用 BindStore 注入)。
var entryStore *store.Store

// BindStore 注入 store 實例(server 啟動時呼叫)。
func BindStore(s *store.Store) {
	entryStore = s
}

func init() {
	types.RegisterTool(QueryEntriesDeclaration, func() types.ToolInterface {
		return &QueryEntriesTool{}
	})
}

// QueryEntriesDeclaration 是給 LLM 看的工具宣告。
// 用於決定查詢範圍、把頻道中已記錄的條目(待辦/行程/會議等)載入到前端的
// 旅程清單表格——查到的結果不會回傳給 LLM 閱讀,LLM 只會收到一句確認文字,
// 實際內容由前端表格顯示,供使用者查看與編輯。
var QueryEntriesDeclaration = types.ToolDeclaration{
	Name: "entry_query",
	Description: "依時間範圍查詢頻道中已記錄的條目(待辦、行程、會議等),把查到的結果載入到前端的旅程清單表格供使用者查看與編輯。" +
		"當使用者在提問、想知道某段時間有什麼安排,或你在記錄新條目前需要確認是否已經記過同一件事時呼叫。可用 from / to 限定時間範圍。" +
		"注意:這個工具不會把查到的條目內容回傳給你——回傳的只是一句確認文字(如「已載入 3 筆到前端表格」)與筆數,實際內容顯示在前端表格,不能拿來回答使用者或做任何判斷。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"from": map[string]interface{}{
				"type": "STRING",
				"description": "範圍起點,用【英文】自然語言時間語詞表達(系統自動換算)。" +
					"把時間範圍拆成起點與終點兩個時間點,例如「上週」→ from='last Monday'、to='last Sunday';" +
					"「下個月」→ from='next month' 的起訖兩日。可用 'last Monday'、'tomorrow'、'June 1' 等。" +
					"不限定起點就留空字串。",
			},
			"to": map[string]interface{}{
				"type": "STRING",
				"description": "範圍終點(含),格式同 from(英文時間語詞)。" +
					"查單一天時 from 與 to 填同一個英文語詞即可。不限定終點就留空字串。",
			},
		},
	},
}

type QueryEntriesTool struct {
	types.BaseToolConfig
}

// Call 執行查詢:回傳 []ResultContentBlock(want 新規格),結果 map 以 ctx.EmitToolResult 發送。
// 查到的條目不進入回傳內容——轉換成 TripEntryPayload 後透過 NotifyEntriesLoaded
// 廣播給前端,回傳與 EmitToolResult 都只帶簡短確認文字與筆數。
func (t *QueryEntriesTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	// from / to 是 LLM 給的英文日期語詞(範圍以兩個日期點表達),用 when 換算成絕對日期。
	// 查詢只需日期粒度,不需時刻。
	now := time.Now()
	rawFrom, rawTo := args.GetString("from"), args.GetString("to")
	from := resolveDate(rawFrom, now)
	to := resolveDate(rawTo, now)

	// 範圍頭尾顛倒(from 晚於 to)→ 回 error 讓 agent 自己修正,而非默默對換。
	// 訊息給出解析後的實際日期,讓 agent 知道哪個語詞算錯、該怎麼改。
	if from != "" && to != "" && from > to {
		return nil, fmt.Errorf(
			"時間範圍顛倒:from '%s' 解析為 %s,晚於 to '%s' 解析為 %s。"+
				"請改用不會顛倒的語詞重新查詢(例如查「這週」用 from='last Monday'、to='next Sunday'),"+
				"確保 from 的日期早於或等於 to。今天是 %s。",
			rawFrom, from, rawTo, to, now.Format("2006-01-02"))
	}

	if entryStore == nil {
		return nil, fmt.Errorf("store 未初始化")
	}
	// channelID 取自本次呼叫的 SessionEnvs(agent 不需自己帶)。
	channelID := ChannelFrom(ctx)
	entries, err := entryStore.ListEntriesByRange(channelID, from, to)
	if err != nil {
		return nil, fmt.Errorf("查詢條目失敗: %w", err)
	}

	payload := toTripEntryPayloads(entries)
	NotifyEntriesLoaded(channelID, payload)

	var confirm string
	if len(entries) == 0 {
		confirm = "查無符合條目"
	} else {
		confirm = fmt.Sprintf("已載入 %d 筆到前端表格", len(entries))
	}

	ctx.EmitToolResult(map[string]interface{}{
		"summary": confirm,
		"count":   len(entries),
	})
	return []types.ResultContentBlock{types.TextBlock(confirm)}, nil
}

// toTripEntryPayloads 把 store 查到的 model.Entry 轉成前端 TripEntry 格式
// (TripEntryPayload,見 sink.go),欄位對齊 server/tools/clienttools.yaml 的
// title/date/time/note 命名——date 取 model.Entry.Start、time 取 StartTime
// (model.Entry 的日期/時刻本就分開存放,直接對應,不需另外拆解字串)。
// Note 是 *string(可為 nil),nil 時轉成空字串,對齊 TripEntry.note 恆為 string
// 的欄位型別(前端沒有「未設定」與「空字串」的區別)。
func toTripEntryPayloads(entries []model.Entry) []TripEntryPayload {
	out := make([]TripEntryPayload, 0, len(entries))
	for _, e := range entries {
		note := ""
		if e.Note != nil {
			note = *e.Note
		}
		out = append(out, TripEntryPayload{
			ID:    e.ID,
			Title: e.Title,
			Date:  e.Start,
			Time:  e.StartTime,
			Note:  note,
		})
	}
	return out
}

func (t *QueryEntriesTool) RenderToolUse(args types.ToolArguments) string {
	from, to := args.GetString("from"), args.GetString("to")
	if from == "" && to == "" {
		return "正在查詢全部條目"
	}
	return fmt.Sprintf("正在查詢條目:%s ~ %s", from, to)
}

func (t *QueryEntriesTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("查詢條目失敗:%v", err)
}

func (t *QueryEntriesTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["summary"].(string); ok {
		return msg
	}
	return "查詢完成"
}
