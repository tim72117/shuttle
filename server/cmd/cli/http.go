package main

// http.go 走 /internal/ API 的實作，供連接遠端或本地 server 使用。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/channel/server/internal/tripsvc"
)

type httpClient struct {
	base string // e.g. http://localhost:8080
}

func newHTTPClient(base string) *httpClient {
	return &httpClient{base: base}
}

func (c *httpClient) do(method, path string, body any) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error %d: %v", resp.StatusCode, result)
	}
	return result, nil
}

func (c *httpClient) listChannels() (any, error) {
	return c.do("GET", "/internal/channels", nil)
}

func (c *httpClient) record(channelID, item, start, startTime, end, endTime, location string) (any, error) {
	return c.do("POST", "/internal/channels/"+channelID+"/entries", map[string]any{
		"item": item, "start": start, "startTime": startTime,
		"end": end, "endTime": endTime, "location": location,
	})
}

func (c *httpClient) addToTrip(entryID, tripID, title string) (string, string, error) {
	res, err := c.do("POST", "/internal/entries/"+entryID+"/trip", map[string]any{
		"tripID": tripID, "title": title,
	})
	if err != nil {
		return "", "", err
	}
	tid, _ := res["tripID"].(string)
	// channelID 不從 HTTP 回傳（notify 已在 server 端處理）
	return tid, "", nil
}

func (c *httpClient) listTrips(channelID string) (any, error) {
	return c.do("GET", "/internal/channels/"+channelID+"/trips", nil)
}

func (c *httpClient) tripEntries(channelID, tripID string) (any, error) {
	return c.do("GET", "/internal/channels/"+channelID+"/trips/"+tripID+"/entries", nil)
}

func (c *httpClient) candidates(channelID, start, end string) (any, error) {
	// candidates 查詢目前只有 DB 直連支援，HTTP 版回傳空
	return map[string]any{"candidates": []any{}}, nil
}

func (c *httpClient) updateEntry(in tripsvc.UpdateEntryInput) error {
	_, err := c.do("PATCH", "/internal/entries/"+in.ID, map[string]any{
		"item": in.Item, "start": in.Start, "end": in.End,
		"location": in.Location, "summary": in.Summary,
		"kind": in.Kind, "detail": in.Detail,
	})
	return err
}

func (c *httpClient) deleteEntry(entryID string) error {
	_, err := c.do("DELETE", "/internal/entries/"+entryID, nil)
	return err
}

func (c *httpClient) deleteTrip(tripID string) error {
	_, err := c.do("DELETE", "/internal/trips/"+tripID, nil)
	return err
}

func (c *httpClient) reset(channelID string) error {
	_, err := c.do("DELETE", "/internal/channels/"+channelID+"/entries", nil)
	return err
}
