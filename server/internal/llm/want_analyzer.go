package llm

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/channel/server/internal/model"
	// 自訂 want 工具(record_entry):init() 註冊工具,並提供記錄 context/sink。
	"github.com/channel/server/internal/wanttools"

	wantconfig "github.com/tim72117/want/config"
	wantorch "github.com/tim72117/want/orchestrator"
	wanttypes "github.com/tim72117/want/types"
	wantui "github.com/tim72117/want/ui"
)

// WantAnalyzer 用 want 的 orchestrator(會自行載入 provider)實作 Analyzer。
// orchestrator 是事件驅動的:Submit 送出 prompt,透過 EventBus 收回應,
// 這裡把它包成同步的「prompt → text」。
type WantAnalyzer struct {
	orch *wantorch.Orchestrator
	// orchestrator 共享單一 agent 狀態,序列化呼叫避免交錯。
	mu sync.Mutex
}

// NewWant 用 channel 自己的環境變數組裝 want 的 Settings,直接呼叫
// SetupWith(純函式,不讀檔、不讀 env、不碰全域)初始化 orchestrator。
// channel 作為「嵌入呼叫 want」的宿主,自行決定設定來源與機密存放,
// 不依賴 want 的 configs/settings.json 路徑假設。
func NewWant() (*WantAnalyzer, error) {
	wd, _ := os.Getwd()
	wanttypes.InitialWorkingDir = wd

	settings := &wantconfig.Settings{
		Provider:        os.Getenv("AI_PROVIDER"),
		Model:           os.Getenv("AI_MODEL"),
		VLLMBaseURL:     os.Getenv("VLLM_BASE_URL"),
		OllamaURL:       os.Getenv("OLLAMA_URL"),
		GoogleAPIKey:    os.Getenv("GOOGLE_API_KEY"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
	}

	orch := wantorch.SetupWith(settings, "assistant")
	orch.OnError(func(err error) {
		fmt.Printf("[want] 🔴 Agent Error: %v\n", err)
	})

	// 掛載各工具自帶的服務路由(同 web/server.go)。
	wanttypes.MountServices()

	return &WantAnalyzer{orch: orch}, nil
}

// generate 送一個 prompt,收集本次推論的完整文字回應。
// 事件解析沿用 want 官方的 ui.HandleInferenceMessage(與 web/server.go 一致):
// TextViewModel 為 assistant 文字;StatusViewModel{Status:"idle"} 表示推論結束。
func (w *WantAnalyzer) generate(prompt string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	state := wantui.NewCommonInferenceState()
	var mu sync.Mutex
	var sb strings.Builder
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	unsub := w.orch.EventBus.Subscribe("agent.inference", func(payload interface{}) {
		mu.Lock()
		defer mu.Unlock()

		result, handled := wantui.HandleInferenceMessage(payload, state)
		if !handled || result == nil {
			return
		}
		switch vm := result.(type) {
		case wantui.TextViewModel:
			// 收到 assistant 文字即視為拿到答案。
			// (事件順序可能是 ...→idle→AgentMessage,文字會晚於 idle 到達。)
			if vm.Content != "" {
				sb.WriteString(vm.Content)
				finish()
			}
		case wantui.StatusViewModel:
			// idle 表示推論結束;但文字可能緊接其後才到,
			// 故延遲一小段再結束,給文字事件到達窗口。
			if vm.Status == "idle" {
				go func() {
					time.Sleep(1500 * time.Millisecond)
					finish()
				}()
			}
		}
	})
	defer unsub()

	w.orch.Submit(prompt)

	select {
	case <-done:
		mu.Lock()
		defer mu.Unlock()
		return sb.String(), nil
	case <-time.After(90 * time.Second):
		return "", fmt.Errorf("want 推論逾時")
	}
}

// AssistEntry 是 agent 用 present_entries 輸出、要展示給使用者的條目。
// 與 wanttools.PresentedEntry 同形,但定義在 llm 層,讓 api 不需依賴 wanttools。
type AssistEntry struct {
	Item      string `json:"item"`
	Start     string `json:"start"`
	StartTime string `json:"startTime"`
	End       string `json:"end"`
	EndTime   string `json:"endTime"`
}

// AssistResult 是 owner 統一輸入的處理結果。
// Kind: "recorded"(agent 記錄了條目)或 "answer"(agent 回答了問題)。
type AssistResult struct {
	Kind     string        // "recorded" | "answer"
	Text     string        // agent 的文字回應(已記錄時為確認語;回答時為答案)
	Logged   int           // 本次記錄的條目數
	EntryIDs []string      // 本次記錄時 emit 同步寫入的 entry ID(供前端關聯/更新顯示)
	Entries  []AssistEntry // agent 用 present_entries 輸出的展示條目(供前端列表顯示)
}

// Assist 統一處理 owner 的輸入:在同一次完整 agent 推論裡,讓 LLM 自主決定
// 「這是提問就回答、是要記的事項就呼叫 record_entry」。
// 合併 generate(收集文字回應)與 RecordForMessage(設 context、允許工具、等 idle):
//   - 設 record context → agent 若 call record_entry,Entry 自動關聯到 messageID
//   - 跑完後以 wanttools.EmitCount() 判斷究竟記錄了還是只回答了
//
// linkMessage:agent 記錄了條目時,寫入來源 message 並把它與本次 emit 的
// entry(參數 entryIDs)建立多對多關聯。只回答時不呼叫。由 api 層提供(持有 store)。
func (w *WantAnalyzer) Assist(channelID, messageID, text string, linkMessage func(entryIDs []string) error) AssistResult {
	w.mu.Lock()
	defer w.mu.Unlock()

	wanttools.RecordLock()
	defer wanttools.RecordUnlock()
	// 輔助資訊(channelID/messageID)透過 SessionEnvs 隨 ToolUseContext 傳遞給工具,
	// 不進送給 LLM 的 prompt,也不經過任何套件級全域變數。
	w.orch.SetSessionEnvs(map[string]string{"channelID": channelID, "messageID": messageID})

	state := wantui.NewCommonInferenceState()
	var mu sync.Mutex
	var sb strings.Builder
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	unsub := w.orch.EventBus.Subscribe("agent.inference", func(payload interface{}) {
		mu.Lock()
		defer mu.Unlock()
		result, handled := wantui.HandleInferenceMessage(payload, state)
		if !handled || result == nil {
			return
		}
		switch vm := result.(type) {
		case wantui.TextViewModel:
			if vm.Content != "" {
				sb.WriteString(vm.Content)
			}
		case wantui.StatusViewModel:
			// idle 表示推論結束;給工具呼叫/文字事件一點到達窗口再結束。
			if vm.Status == "idle" {
				go func() { time.Sleep(1500 * time.Millisecond); finish() }()
			}
		}
	})
	defer unsub()

	w.orch.Submit(text)

	select {
	case <-done:
	case <-time.After(90 * time.Second):
		fmt.Printf("[want] Assist 逾時\n")
	}

	mu.Lock()
	answer := strings.TrimSpace(sb.String())
	mu.Unlock()

	// 把 wanttools 的展示條目轉成 llm 層型別(讓 api 不需依賴 wanttools)。
	var presented []AssistEntry
	for _, e := range wanttools.Presented() {
		presented = append(presented, AssistEntry{
			Item: e.Item, Start: e.Start, StartTime: e.StartTime, End: e.End, EndTime: e.EndTime,
		})
	}

	logged := wanttools.EmitCount()
	if logged > 0 {
		// agent 記錄了條目:條目已由 emit 同步寫入(entry 為主體,獨立落庫於後端)。
		// 原話(message)不存後端,由前端存進裝置端 DB,故此處不寫 message。
		// linkMessage 保留為相容參數:若呼叫端提供(目前傳 nil),仍可建立關聯;
		// 預設不呼叫。回傳本次寫入的 entry ID 供前端更新顯示。
		ids := wanttools.EmittedIDs()
		if linkMessage != nil {
			if err := linkMessage(ids); err != nil {
				return AssistResult{Kind: "error", Text: "寫入訊息/關聯失敗: " + err.Error()}
			}
		}
		return AssistResult{Kind: "recorded", Text: answer, Logged: logged, EntryIDs: ids, Entries: presented}
	}
	return AssistResult{Kind: "answer", Text: answer, Logged: 0, Entries: presented}
}

// Answer 讓 want agent 回答自然語言查詢:agent 依 assistant.md 指引,自己呼叫
// query_entries 查條目、再對每筆相關條目呼叫 present_entries 呈現
// (不用 citeEntries 關鍵字比對,也不把 pool 塞進 prompt)。
// 跑完用 Presented() 取 agent 呈現的結構化條目,確保卡片與文字來自同一判斷。
// channelID 必填:query_entries 工具靠 SessionEnvs 得知要查哪個頻道。
func (w *WantAnalyzer) Answer(channelID, question string) model.SearchAnswer {
	w.mu.Lock()
	defer w.mu.Unlock()

	// present_entries 把結果累積進 wanttools.presented;RecordLock 會先重置,
	// 跑完用 Presented() 取本次 agent 透過 present_entries 呈現的條目。
	wanttools.RecordLock()
	defer wanttools.RecordUnlock()
	// 讓 query_entries 知道查哪個頻道(同 Assist 路徑;查詢不關聯 message,故不設 messageID)。
	w.orch.SetSessionEnvs(map[string]string{"channelID": channelID})

	state := wantui.NewCommonInferenceState()
	var mu sync.Mutex
	var sb strings.Builder
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	unsub := w.orch.EventBus.Subscribe("agent.inference", func(payload interface{}) {
		mu.Lock()
		defer mu.Unlock()
		result, handled := wantui.HandleInferenceMessage(payload, state)
		if !handled || result == nil {
			return
		}
		switch vm := result.(type) {
		case wantui.TextViewModel:
			if vm.Content != "" {
				sb.WriteString(vm.Content)
			}
		case wantui.StatusViewModel:
			// idle 表推論結束;給工具呼叫/文字事件一點到達窗口再結束。
			if vm.Status == "idle" {
				go func() { time.Sleep(1500 * time.Millisecond); finish() }()
			}
		}
	})
	defer unsub()

	w.orch.Submit(question)

	select {
	case <-done:
	case <-time.After(90 * time.Second):
		fmt.Printf("[want] Answer 逾時\n")
	}

	mu.Lock()
	answer := strings.TrimSpace(sb.String())
	mu.Unlock()

	// 取 agent 透過 present_entries 呈現的結構化條目(LLM 自己挑的)。
	var presented []model.PresentedEntry
	for _, e := range wanttools.Presented() {
		presented = append(presented, model.PresentedEntry{
			Item: e.Item, Start: e.Start, StartTime: e.StartTime, End: e.End, EndTime: e.EndTime,
		})
	}

	conf := 0.85
	return model.SearchAnswer{
		Answer:          answer,
		CitedMessageIDs: []string{},
		Confidence:      &conf,
		Entries:         presented,
	}
}

// ---- 解析 ----
// 使用者輸入原封不動送給 agent,不再手動拼接日期等上下文。
// 「今天」的基準點怎麼提供給 agent(相對日期換算用)待後續處理。
