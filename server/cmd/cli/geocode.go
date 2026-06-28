package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/channel/server/internal/geo"
)

func cmdGeocode(args []string) {
	fs := flag.NewFlagSet("geocode", flag.ExitOnError)
	place := fs.String("place", "", "地點名稱（必填）")
	region := fs.String("region", "", "國家代碼限制，如 jp / tw / cn（可選）")
	maxN := fs.Int("n", 1, "回傳候選筆數（1-5，預設 1）")
	entryID := fs.String("entry", "", "寫入座標的 entry ID（指定時取第一筆自動寫入）")
	apiURLFlag := fs.String("api", "http://localhost:8080", "server URL")
	_ = fs.Parse(args)

	if *place == "" && fs.NArg() > 0 {
		*place = fs.Arg(0)
	}
	if *place == "" {
		fatal("geocode 需要 -place 地點名稱")
	}

	apiKey := os.Getenv("GOOGLE_PLACES_API_KEY")
	client := geo.New(apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	places, err := client.Search(ctx, *place, &geo.SearchOptions{
		Region:     *region,
		MaxResults: *maxN,
	})
	if err != nil {
		fatal("geocode: %v", err)
	}

	// 有指定 entry ID 就把第一筆座標寫入
	if *entryID != "" {
		first := places[0]
		body, _ := json.Marshal(map[string]any{"lat": first.Lat, "lng": first.Lng})
		url := fmt.Sprintf("%s/internal/entries/%s/latlng", *apiURLFlag, *entryID)
		req, err := http.NewRequest("PATCH", url, bytes.NewReader(body))
		if err != nil {
			fatal("geocode set-latlng: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			fatal("geocode set-latlng: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			fatal("geocode set-latlng: server 回傳 %d", res.StatusCode)
		}
	}

	output(map[string]any{
		"query":      *place,
		"region":     *region,
		"entryID":    *entryID,
		"candidates": places,
	})
}
