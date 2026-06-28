// Package tripsvc 是 entry/trip 的「行程服務層」:把記錄條目、找候選行程、
// 歸入行程、以及 trip CRUD 的邏輯集中於此,作為單一真實來源。
//
// 設計原則:LLM 工具(wanttools)與 CLI(cmd/seedtrip)都只薄薄呼叫本層,
// 不各自重刻歸組邏輯。互動模式對齊 LLM:
//
//  1. Record(記錄一筆 entry)→ 回傳 entryID + 時間重疊的「候選行程」
//  2. 呼叫端(LLM 或 Claude Code)依語意判斷該 entry 是否屬於某候選
//  3. AddToTrip(entryID, tripID)→ 歸入(tripID 留空則新建)
//
// 刻意「不自動歸組」:歸或不歸由呼叫端決定,與 LLM 真實路徑一致。
package tripsvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"

	"github.com/channel/server/internal/geo"
	"github.com/channel/server/internal/model"
	"github.com/channel/server/internal/store"
)

func nowUTC() time.Time { return time.Now().UTC() }

// Service 持有 store,提供 entry/trip 的行程操作。
type Service struct {
	st  *store.Store
	geo *geo.Client
}

// New 建立服務。
func New(st *store.Store, geoClient *geo.Client) *Service {
	return &Service{st: st, geo: geoClient}
}

// newEntryID 產生 entry ID(對齊既有 ent_ 風格)。
func newEntryID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "ent_" + hex.EncodeToString(b)
}

// RecordInput 是記錄一筆條目的輸入。
type RecordInput struct {
	ChannelID string
	Item      string
	Start     string // 'YYYY-MM-DD';可空
	StartTime string // 'HH:MM';空=全日
	End       string // 'YYYY-MM-DD';可空
	EndTime   string // 'HH:MM';可空
	Location  string // 可空
}

// RecordResult 是記錄結果:新 entry 與時間重疊的候選行程(供呼叫端判斷歸入)。
type RecordResult struct {
	EntryID    string       `json:"entryID"`
	Candidates []model.Trip `json:"candidates"` // 時間重疊的既有 trip;空代表無候選
}

// Record 寫入一筆 entry(tripID 留空,不自動歸組),回傳候選行程。
func (s *Service) Record(in RecordInput) (RecordResult, error) {
	id := newEntryID()
	e := model.Entry{
		ID:        id,
		ChannelID: in.ChannelID,
		Item:      in.Item,
		Start:     in.Start,
		StartTime: in.StartTime,
		End:       in.End,
		EndTime:   in.EndTime,
		Location:  in.Location,
		CreatedAt: nowUTC(),
	}
	if err := s.st.InsertEntry(e); err != nil {
		return RecordResult{}, err
	}

	// 有地點時非同步補經緯度，失敗只記 log，不阻擋主流程。
	if in.Location != "" && s.geo != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			lat, lng, err := s.geo.Lookup(ctx, in.Location)
			if err != nil {
				log.Printf("geo lookup %q: %v", in.Location, err)
				return
			}
			if err := s.st.SetEntryLatLng(id, lat, lng); err != nil {
				log.Printf("set lat/lng entry %s: %v", id, err)
			}
		}()
	}

	// 找時間重疊的候選 trip(不歸入,只回給呼叫端判斷)。無時間則無候選。
	var cands []model.Trip
	if in.Start != "" {
		c, err := s.st.FindOverlappingTrips(in.ChannelID, in.Start, in.End)
		if err != nil {
			return RecordResult{}, err
		}
		cands = c
	}
	if cands == nil {
		cands = []model.Trip{}
	}
	return RecordResult{EntryID: id, Candidates: cands}, nil
}

// AddToTrip 把 entry 歸入指定 trip;tripID 留空則以該 entry 的時間/標題新建 trip。
// title 留空時新建用 entry.Item。回傳最終的 tripID。
func (s *Service) AddToTrip(entryID, tripID, title string) (string, string, error) {
	e, err := s.st.GetEntry(entryID)
	if err != nil {
		return "", "", err
	}
	if tripID == "" {
		t := title
		if t == "" {
			t = e.Item
		}
		newID, err := s.st.CreateTrip(e.ChannelID, t, e.Start, e.End)
		if err != nil {
			return "", "", err
		}
		tripID = newID
	}
	if err := s.st.SetEntryTrip(entryID, &tripID); err != nil {
		return "", "", err
	}
	return tripID, e.ChannelID, nil
}

// ---- CRUD 轉發(讓呼叫端不必直接依賴 store) ----

// ListTrips 回頻道的所有行程。
func (s *Service) ListTrips(channelID string) ([]model.Trip, error) {
	return s.st.ListTripsByChannel(channelID)
}

// ListTripEntries 回某行程底下的所有條目。
func (s *Service) ListTripEntries(channelID, tripID string) ([]model.Entry, error) {
	return s.st.ListEntriesByTrip(channelID, tripID)
}

// FindCandidates 查時間重疊的候選行程(供「先 record 後 add」之外的查詢用)。
func (s *Service) FindCandidates(channelID, start, end string) ([]model.Trip, error) {
	return s.st.FindOverlappingTrips(channelID, start, end)
}

// UpdateEntryInput 是更新條目的輸入，留空欄位不更新。
type UpdateEntryInput struct {
	ID        string
	Item      string
	Start     string
	StartTime string
	End       string
	EndTime   string
	Location  string
	Summary   string
	Kind      string
	Detail    map[string]any
}

// UpdateEntry 更新一筆 entry 的可編輯欄位。
func (s *Service) UpdateEntry(in UpdateEntryInput) error {
	return s.st.UpdateEntry(in.ID, in.Item, in.Start, in.StartTime, in.End, in.EndTime, in.Location, in.Summary, in.Kind, in.Detail)
}

// DeleteTrip 刪除單一行程(解除底下 entries 的 tripID,不刪 entries 本身)。
func (s *Service) DeleteTrip(tripID string) error {
	return s.st.DeleteTrip(tripID)
}

// Reset 清空頻道的所有 entries 與 trips(開發/測試用)。
func (s *Service) Reset(channelID string) error {
	return s.st.DeleteChannelEntriesAndTrips(channelID)
}
