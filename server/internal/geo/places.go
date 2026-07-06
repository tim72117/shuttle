// Package geo 封裝 Google Places API (New)（Text Search），
// 輸入地點名稱，回傳候選地點清單（含經緯度）。
package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// 新版 Places API (New) 的 Text Search 端點(POST)。
// 舊版為 maps.googleapis.com/maps/api/place/textsearch/json(GET),已於 2026 遷移至此。
const placesURL = "https://places.googleapis.com/v1/places:searchText"

// fieldMask 指定新版 API 要回傳哪些欄位(新版必填 header X-Goog-FieldMask,
// 不給會回 400)。只取目前用到的:顯示名稱、格式化地址、經緯度。
const fieldMask = "places.displayName,places.formattedAddress,places.location"

// Client 持有 API key，提供地點查詢。
type Client struct {
	apiKey string
	http   *http.Client
}

// New 建立 Client；apiKey 為空時 Search 永遠回傳 ErrNoKey。
func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 5 * time.Second},
	}
}

var ErrNoKey = fmt.Errorf("geo: Google Places API key 未設定")
var ErrNotFound = fmt.Errorf("geo: 找不到符合的地點")

// Place 是候選地點結果。
type Place struct {
	Name    string  `json:"name"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

// SearchOptions 是查詢選項。
type SearchOptions struct {
	// Region 是 ISO 3166-1 alpha-2 國家代碼（如 "jp"、"tw"、"cn"），
	// 讓結果優先偏向該國。空字串表示不限制。
	Region string
	// MaxResults 最多回傳幾筆候選，預設 1，最大 5。
	MaxResults int
}

// Search 查詢地點名稱，回傳候選清單。
// opts 可傳 nil 使用預設值（只回傳第一筆，不限地區）。
func (c *Client) Search(ctx context.Context, place string, opts *SearchOptions) ([]Place, error) {
	if c.apiKey == "" {
		return nil, ErrNoKey
	}
	if place == "" {
		return nil, ErrNotFound
	}

	maxN := 1
	region := ""
	if opts != nil {
		if opts.MaxResults > 0 {
			maxN = opts.MaxResults
			if maxN > 5 {
				maxN = 5
			}
		}
		region = opts.Region
	}

	// 新版:參數放 JSON body。pageSize 對應舊版 MaxResults;
	// regionCode 對應舊版 region(新版用大寫國碼,如 "JP")。
	reqBody := map[string]any{
		"textQuery": place,
		"pageSize":  maxN,
	}
	if region != "" {
		reqBody["regionCode"] = region
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", placesURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", fieldMask)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geo: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 新版錯誤 body 含 {"error":{"message":...}},取出便於排查(如 key 無權限、未啟用服務)。
		var errBody struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if errBody.Error.Message != "" {
			return nil, fmt.Errorf("geo: request failed (HTTP %d): %s", resp.StatusCode, errBody.Error.Message)
		}
		return nil, fmt.Errorf("geo: request failed (HTTP %d)", resp.StatusCode)
	}

	// 新版回應結構:places[].displayName.text / formattedAddress / location.{latitude,longitude}
	var body struct {
		Places []struct {
			DisplayName struct {
				Text string `json:"text"`
			} `json:"displayName"`
			FormattedAddress string `json:"formattedAddress"`
			Location         struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"location"`
		} `json:"places"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("geo: decode failed: %w", err)
	}
	if len(body.Places) == 0 {
		return nil, ErrNotFound
	}

	out := make([]Place, 0, maxN)
	for i, p := range body.Places {
		if i >= maxN {
			break
		}
		out = append(out, Place{
			Name:    p.DisplayName.Text,
			Address: p.FormattedAddress,
			Lat:     p.Location.Latitude,
			Lng:     p.Location.Longitude,
		})
	}
	return out, nil
}

// Lookup 查詢地點名稱，回傳第一筆結果的經緯度（向下相容用）。
func (c *Client) Lookup(ctx context.Context, place string) (lat, lng float64, err error) {
	places, err := c.Search(ctx, place, nil)
	if err != nil {
		return 0, 0, err
	}
	return places[0].Lat, places[0].Lng, nil
}
