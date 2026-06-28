package llm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/channel/server/internal/model"
	"github.com/channel/server/internal/store"
)

// MockAnalyzer 是不接真實 LLM 的假分析器,供 web 介面實際操作、看完整資料流。
// 與 want 引擎不同:它不解析使用者輸入——前端送出的文字只是「觸發」,
// mock 自己從預設情境輪流產生 entry,走與真 LLM 完全相同的落庫路徑
// (FindOrCreateTrip 歸組 + InsertEntry),故 Trip 聚合、查詢都真實運作。
//
// 實作 Analyzer(Answer)與 Assistant(AssistForSession)兩個 interface,
// 可直接取代 WantPool 注入。
type MockAnalyzer struct {
	store *store.Store
	mu    sync.Mutex
	// next 是預設情境的游標:每次記事取一筆,循環。
	next int
}

// NewMock 建立 mock 分析器(需 store 以真實寫入 entry / 觸發 Trip 歸組)。
func NewMock(st *store.Store) *MockAnalyzer {
	return &MockAnalyzer{store: st}
}

// scenario 是預設情境的一筆。日期用「相對今天的天數」表示,記事當下換算成絕對日期,
// 讓 entries 永遠落在「未來」。刻意設計成可展示 Trip 歸組:
//   - 東京旅遊:住宿(區間,框出行程)+ 機票、開會(落在範圍內 → 歸入同一 Trip)
//   - 看牙醫(範圍外 → 自成一個 Trip)
//   - 買牛奶(無時間 → 不歸組)
type scenario struct {
	item     string
	startDay int    // 距今天數;-1 表示無時間
	endDay   int    // 距今天數;<0 表示無 end(單點)
	clock    string // 'HH:MM';空表全日
}

var mockScenarios = []scenario{
	{item: "東京住宿(新宿飯店)", startDay: 7, endDay: 10, clock: ""},     // 區間:7~10 天後
	{item: "東京來回機票", startDay: 7, endDay: -1, clock: "09:30"},    // 落在住宿首日
	{item: "與客戶開會討論合約", startDay: 8, endDay: -1, clock: "14:00"}, // 落在住宿範圍內
	{item: "看牙醫", startDay: 30, endDay: -1, clock: "10:00"},      // 範圍外 → 自成 Trip
	{item: "買牛奶", startDay: -1, endDay: -1, clock: ""},           // 無時間 → 不歸組
}

func newEntryID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "ent_" + hex.EncodeToString(b)
}

// fmtDate 把「距今天數」換算成日期字串。
func fmtDate(day int) string {
	if day < 0 {
		return ""
	}
	return time.Now().UTC().AddDate(0, 0, day).Format("2006-01-02")
}

// AssistForSession 模擬 owner 記事:取下一筆預設情境寫成 entry。不解析 text(僅作觸發)。
//
// 走與 want 真實 LLM 一致的新流程(不再用 FindOrCreateTrip 自動歸組):
//  1. 寫 entry,tripID 留 nil(等同 record_entry)。
//  2. 查時間相符的候選 trip(等同 record_entry 列候選)。
//  3. 模擬「LLM 的決定」:有候選就歸入第一個;無候選但有時間則新建 trip。
//     (等同 LLM 判斷後呼叫 add_to_trip。mock 的規則是「有候選就歸入」。)
func (m *MockAnalyzer) AssistForSession(_, channelID, _, _ string, _ func(entryIDs []string) error) AssistResult {
	m.mu.Lock()
	sc := mockScenarios[m.next%len(mockScenarios)]
	m.next++
	m.mu.Unlock()

	startDate := fmtDate(sc.startDay)
	startTime := sc.clock
	var endDate string
	if sc.endDay >= 0 {
		endDate = fmtDate(sc.endDay)
	}

	// 1. 寫 entry(tripID 留 nil,不自動歸組)。
	id := newEntryID()
	if err := m.store.InsertEntry(model.Entry{
		ID:        id,
		ChannelID: channelID,
		Item:      sc.item,
		Start:     startDate,
		StartTime: startTime,
		End:       endDate,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return AssistResult{Kind: "error", Text: "mock 寫入失敗: " + err.Error()}
	}

	// 2~3. 有時間才考慮歸組:查候選,模擬 LLM 決定(有候選歸入第一個,否則新建)。
	if startDate != "" {
		candidates, err := m.store.FindOverlappingTrips(channelID, startDate, endDate)
		if err == nil {
			var tripID string
			if len(candidates) > 0 {
				tripID = candidates[0].ID // 模擬 LLM 判斷「屬於這個行程」
			} else {
				tripID, _ = m.store.CreateTrip(channelID, sc.item, startDate, endDate) // 模擬 LLM 新建
			}
			if tripID != "" {
				_ = m.store.SetEntryTrip(id, &tripID)
			}
		}
	}

	return AssistResult{
		Kind:     "recorded",
		Text:     "已記錄(mock):" + sc.item,
		Logged:   1,
		EntryIDs: []string{id},
	}
}

// Answer 模擬查詢:讀該頻道現有 entries,整理成回答 + 展示條目。
// 不解析 question(僅作觸發);回真實的頻道條目。
func (m *MockAnalyzer) Answer(channelID, _ string) model.SearchAnswer {
	entries, err := m.store.ListEntriesByChannel(channelID)
	if err != nil || len(entries) == 0 {
		return model.SearchAnswer{
			Answer:  "(mock)這個頻道目前沒有記錄的安排。",
			Entries: []model.PresentedEntry{},
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("(mock)這個頻道目前有 %d 筆安排:\n", len(entries)))
	presented := make([]model.PresentedEntry, 0, len(entries))
	for _, e := range entries {
		when := e.Start
		if when == "" {
			when = "(未指定時間)"
		}
		sb.WriteString("・" + when + " " + e.Item + "\n")
		presented = append(presented, model.PresentedEntry{
			Item:      e.Item,
			Start:     e.Start,
			StartTime: e.StartTime,
			End:       e.End,
			EndTime:   e.EndTime,
		})
	}

	return model.SearchAnswer{
		Answer:  strings.TrimRight(sb.String(), "\n"),
		Entries: presented,
	}
}
