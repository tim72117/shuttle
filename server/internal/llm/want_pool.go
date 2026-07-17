package llm

import (
	"sync"

	"github.com/tim72117/shuttle/internal/model"
)

// WantPool 是 per-session orchestrator 的「外殼」。
//
// 目標架構(路 2):每個 session 一個獨立的 WantAnalyzer(各自 orchestrator),
// 互不干擾。但 want 引擎目前是「全域單例」設計(GlobalEngine / GlobalEventBus /
// GlobalAppStore),同一 process 內多個 orchestrator 會共用全域狀態而互相污染,
// 所以「每 session 一個獨立 orchestrator」要等 want 改造成「實例化引擎」後才能真正做到。
//
// 現階段(want 未改造):For() 一律回傳同一個共用 WantAnalyzer,
// 行為與改造前完全相同(共用、序列化)。骨架先就位——want 改造完成後,
// 只需把 For() 裡「回傳 shared」改成「沒有就 newSessionAnalyzer() 建一個」即可,
// server 其餘程式不必更動。
//
// WantPool 自己也實作 Analyzer interface,讓「無 session 資訊」的舊呼叫路徑
// 仍可把它當成單一 analyzer 使用(轉發到 shared)。
type WantPool struct {
	mu sync.Mutex
	// byID 是未來 per-session 實例的存放處。want 改造前永遠是空的。
	byID map[string]*WantAnalyzer
	// shared 是現階段所有 session 共用的唯一實例。
	shared *WantAnalyzer
}

// NewWantPool 初始化共用的 WantAnalyzer 並包成 pool。
// 失敗(want 初始化失敗)時回傳 error,呼叫端可決定退回規則式。
func NewWantPool() (*WantPool, error) {
	shared, err := NewWant()
	if err != nil {
		return nil, err
	}
	return &WantPool{
		byID:   make(map[string]*WantAnalyzer),
		shared: shared,
	}, nil
}

// For 取得指定 session 對應的 WantAnalyzer。
//
// 現階段:忽略 sessionID,一律回傳共用實例(維持現有行為)。
// want 改造後:改為「byID 查不到就建一個獨立 orchestrator 存入」即可達成 per-session。
func (p *WantPool) For(sessionID string) *WantAnalyzer {
	p.mu.Lock()
	defer p.mu.Unlock()

	// ── want 改造後在此啟用 per-session ──────────────────────────
	// if a, ok := p.byID[sessionID]; ok {
	// 	return a
	// }
	// a, err := newSessionAnalyzer(sessionID) // 需 want 支援實例化引擎
	// if err == nil {
	// 	p.byID[sessionID] = a
	// 	return a
	// }
	// ────────────────────────────────────────────────────────────
	_ = sessionID
	return p.shared
}

// ---- Analyzer interface:無 session 資訊的呼叫路徑轉發到共用實例 ----
// 讓 WantPool 可直接當成 Analyzer 注入。

func (p *WantPool) Answer(channelID, question, lang string) model.SearchAnswer {
	return p.For("").Answer(channelID, question, lang)
}

// AssistForSession 依 session 取 analyzer,統一處理 owner 輸入(LLM 自主判斷回答/記錄)。
// 提問時 agent 自己用 query_entries 工具查條目,無需傳入頻道訊息。
// lang 是使用者設定的回答語言偏好("zh-TW"/"en"),空字串視為預設(繁體中文)。
// linkMessage:當 agent 決定「記錄」時,寫入來源 message 並與本次 emit 的
// entry(entryIDs)建立多對多關聯;agent 只回答時不會被呼叫。
func (p *WantPool) AssistForSession(sessionID, channelID, messageID, text, lang string, linkMessage func(entryIDs []string) error) AssistResult {
	return p.For(sessionID).Assist(channelID, messageID, text, lang, linkMessage)
}

// Assistant 是「統一處理 owner 輸入,LLM 自主判斷回答或記錄」的能力。
// 只有 want 引擎(WantPool)實作;規則式分析器不支援(api handler 以斷言判斷)。
type Assistant interface {
	AssistForSession(sessionID, channelID, messageID, text, lang string, linkMessage func(entryIDs []string) error) AssistResult
}
