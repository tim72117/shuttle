// Package mockllm 提供一個「動態、會讀取前一輪真實執行結果」的假 LLM 引擎,
// 用於端到端測試(驗證「LLM 推論安排整段行程,過程中新增/刪除/修改,前端即時
// 更新」這件事真的能動),但完全不打真實 LLM API(vLLM/Ollama/Claude/…)。
//
// # 為什麼不是 want 套件內建的 provider.AIProvider 直接注入
//
// want v0.0.2(shuttle go.mod 實際解析到的版本;已比對 /Users/caitingyu/Documents/want
// 本機原始碼樹,確認兩者有實質差異——本機原始碼是較舊的修訂,orchestrator.SetupWith/
// config.Settings.MockScenario 等在那份原始碼裡都還不存在;本套件一律以 v0.0.2
// 為準)的 provider.AIProvider 是一個乾淨的 Go 介面
// (internal/provider/provider.go:GenerateStream(ctx, []types.Experience, map[string]any,
// chan any) ([]types.Content, error)),理論上任何實作它的型別都能被當成 provider 用。
//
// 但實際能把一個 provider.AIProvider 值「裝進」執行中的 want orchestrator,
// 只有唯一一條路徑:orchestrator.InitializeWithConfig(settings) → 內部呼叫
// 定義在 want/internal(Go internal 套件規則保護)的 internal.Initialize(aiProvider),
// 寫入 process 全域單例 internal.GlobalEngine。這條路徑本身是一個「provider 名稱
// 字串 → 內建建構子」的封閉 switch(vllm/ollama/googleapis/claude/mock 五選一),
// 沒有任何分支能接受呼叫端自備的 provider.AIProvider 實例;mock 分支寫死呼叫
// provider.NewMockProviderFromFile,只能讀靜態 JSON,無法動態讀取執行期歷史。
//
// 已實測確認:shuttle 端直接 `import "github.com/tim72117/want/internal"`
// 會被 Go 工具鏈在編譯期直接拒絕(use of internal package ... not allowed),
// 這是語言層級的硬限制,不是「沒試過」。internal.GlobalEngine、內部的
// Agent/ToolUseContext/DispatchToolCall 等——凡是「决定下一步呼叫哪個 provider、
// 怎麼把 provider 回傳的 tool_use 派送給實際工具」的整條路徑——全部定義在
// internal,orchestrator 套件本身也沒有任何 SetProvider 類方法或後門(逐一核對
// orchestrator.go/init.go/init_helper.go 的所有 exported 符號,確認過)。
//
// 換句話說:v0.0.2 沒有「繞過字串查表、直接注入任意 AIProvider 實例」的公開入口
// ——這點與使用者原始猜測(可能存在 SetupWithProvider 之類的函式)不同,這裡如實
// 回報「沒有」,而不是硬凑一個。在不修改 want 套件原始碼的前提下,唯一「合法地」
// 讓自訂邏輯決定 want 下一步做什麼的方式,是讓自訂邏輯長在 InitializeWithConfig
// 現有的某個分支「本來就支援連到外部服務」的那一端——也就是 vllm/ollama 分支
// 本身就是「透過 HTTP 打一個 OpenAI/Ollama 相容端點」,協定本身早就是
// provider 與「真正的推論邏輯」之間的公開契約。
//
// 因此本套件採用的方案:讓「假 LLM」變成一個真的監聽 HTTP 的伺服器,實作
// VLLMProvider(internal/provider/vllm.go)期待的 OpenAI 相容
// POST /v1/chat/completions(SSE, "data: {...}"串流,以 "data: [DONE]" 結尾)
// 協定,設定 AI_PROVIDER=vllm + VLLM_BASE_URL=http://127.0.0.1:<mock 埠>
// 讓 shuttle 用完全正常、未修改的 want_analyzer.go NewWant() 啟動路徑連過去。
// want 端(VLLMProvider.prepareRequest,已讀原始碼確認)會把目前為止累積的
// 完整對話歷史(含前幾輪工具真實執行後的 tool_result)序列化進請求的 messages
// 陣列——這正是「動態讀取前一輪真實結果」所需要的資料,只是換了一個真實、
// 已存在、雙方都認得的協定來傳遞,而不是靠 Go 介面呼叫直接傳 []types.Experience。
//
// 這個做法:
//   - 沒有修改 want 套件的任何一行原始碼。
//   - 沒有使用 unsafe/reflect/go:linkname 等繞過 Go 套件可見性規則的手法。
//   - 只使用 want_analyzer.go 已經在讀的公開環境變數(AI_PROVIDER/VLLM_BASE_URL),
//     shuttle 端零程式碼改動即可切換成這個假 LLM。
//   - 「假 LLM」在 want 眼中是一個完全正常的 vLLM 伺服器,orchestrator/Agent.Run
//     內部機制(round 計數、history 累積、工具派送)全部原封不動照常執行。
//
// # 與 want 內建 provider.MockProvider 的差異
//
// provider.MockProvider(want/internal/provider/mock.go)是純線性重播寫死的
// JSON 情境檔:GenerateStream 完全忽略傳入的歷史參數,無法在腳本裡引用「前一輪
// 真實執行結果才會產生的值」(例如 entry_add 執行後才拿得到的 entryID)。
//
// 本套件的 Engine(見 script.go 其餘部分)改為:每一步驟只放「要呼叫的工具名 +
// 固定參數」,並可選擇性帶一條「擷取規則」,描述「從最近一次某工具的執行結果文字
// 用正則撈一個值,填進本步驟的某個參數」。腳本本身仍是純 Go 資料(不是程式碼),
// 但 Engine.Next() 每次都會依「呼叫端傳入的、貨真價實的請求歷史」動態組出下一步——
// 不是重播寫死的字串。
package mockllm

import (
	"fmt"
	"regexp"
)

// Step 是動態劇本的一個步驟:「假 LLM」在某一輪要送出的回應。
//
//   - 若 FinalText 非空:這一輪不呼叫任何工具,直接回一段純文字,且應是腳本的
//     最後一步——want 的 Agent.Run 看到「本輪沒有 tool_use」就會結束整個推論
//     (見 want/internal/query.go 的 for 迴圈:!toolCalledThisRound 就 break)。
//   - 否則:ToolName/Input 是這一輪要送出的 tool_use。Input 裡值為特殊字串
//     "$1" 的欄位,會在送出前依 ExtractFrom 規則替換成擷取到的動態值。
type Step struct {
	ToolName    string
	Input       map[string]interface{}
	ExtractFrom *ExtractRule
	FinalText   string
}

// ExtractRule 描述「從最近一次某工具的執行結果文字裡擷取一個值」。
//
//   - FromToolName:要尋找的執行結果所屬的工具名(對應 OpenAI 協定 messages 陣列裡
//     role=="tool" 訊息的 tool_call_id 所配對的那次 assistant tool_calls 呼叫的
//     function.name;本套件在 Engine.Next 呼叫端(cmd/mockllm)解析請求時,會先建立
//     一份 tool_call_id → tool 名稱的對照表,再比對 role=="tool" 訊息)。
//   - Pattern:對該筆執行結果文字套用的正則,取第 1 個 capture group。
//   - Occurrence:第幾次出現(由舊到新,1-based)。0 或未設代表「最近一次」
//     (由新到舊掃到的第一筆)。用來支援「第 4 步要用『第一筆』entry_add 的
//     entryID,而不是『最近一筆』(此時最近一筆是第二次 entry_add)」這種情境。
type ExtractRule struct {
	FromToolName string
	Pattern      *regexp.Regexp
	Occurrence   int
}

// EntryIDPattern 對應 server/internal/wanttools/entry_add.go 的
//
//	resultMsg := fmt.Sprintf("Recorded (entryID=%s): %s %s", entryID, ...)
//
// 已核對該檔案原始碼確認格式字串完全一致(entryID 前綴固定為 "ent_" + 隨機 hex,
// 由 shuttle cmd/server/main.go 裡 wanttools.BindSink 那顆 closure 產生)。
var EntryIDPattern = regexp.MustCompile(`entryID=([^)]+)\)`)

// ToolResult 是呼叫端(cmd/mockllm 的 HTTP handler)解析完一次請求後,
// 交給 Engine.Next 的「這次請求歷史裡,依序出現過的所有工具執行結果」。
// 順序即歷史順序(舊到新),由呼叫端保證。
type ToolResult struct {
	ToolName string // 呼叫該結果所屬的工具名
	Text     string // 執行結果的文字內容(對應 OpenAI messages 裡 role=="tool" 的 content)
}

// Engine 依序執行 Steps,每一步要送出的 tool_use 若帶 ExtractFrom,
// 會從呼叫端傳入的 []ToolResult(即這次請求歷史裡目前為止所有工具的真實執行結果)
// 動態撈值填入——不是重播寫死的字串。
//
// Engine 本身不做任何 HTTP/IO;它是純粹的「給定歷史,決定下一步」函式,方便獨立測試
// (不需要啟動真的 HTTP server 或真的 want orchestrator 就能驗證腳本邏輯本身)。
type Engine struct {
	steps  []Step
	cursor int
	onStep func(stepIndex int, step Step, resolvedInput map[string]interface{})
}

// NewEngine 用一份劇本建立 Engine。onStep 是可選的觀測鉤子,每次真的要送出一個
// tool_use(或 FinalText)前呼叫一次,供測試程式記錄「LLM 依序呼叫了哪些工具」。
func NewEngine(steps []Step, onStep func(int, Step, map[string]interface{})) *Engine {
	return &Engine{steps: steps, onStep: onStep}
}

// StepResult 是 Engine.Next 的回傳值:要嘛是一個 tool_use(ToolName 非空),
// 要嘛是結束對話的純文字(IsFinal true)。
type StepResult struct {
	ToolName string
	ToolID   string
	Input    map[string]interface{}
	IsFinal  bool
	Text     string
}

// Done 回傳腳本是否已全部跑完(呼叫端可用來判斷「這次請求其實不該再有下一步」,
// 屬於協助抓測試腳本錯誤用,並非正常流程會走到的分支——正常流程的最後一步應該是
// FinalText,Engine 會在那一步就自然結束對話,不會讓 want 端再送下一次請求)。
func (e *Engine) Done() bool { return e.cursor >= len(e.steps) }

// Next 依目前游標取下一步,若有 ExtractFrom 規則,從 history 動態撈值填入。
// history 是「這次請求裡,到目前為止累積的所有工具執行結果」,由呼叫端從
// OpenAI 協定的 messages 陣列解析後傳入(見 cmd/mockllm 的 parseToolHistory)。
func (e *Engine) Next(history []ToolResult) (StepResult, error) {
	if e.Done() {
		// 腳本用完:回空文字結束對話(行為對齊 want 內建 provider.MockProvider
		// 用完腳本後的回退方式)。理論上不應該被呼叫到,見 Done() 的說明。
		return StepResult{IsFinal: true, Text: ""}, nil
	}

	step := e.steps[e.cursor]
	idx := e.cursor
	e.cursor++

	if step.FinalText != "" {
		if e.onStep != nil {
			e.onStep(idx, step, nil)
		}
		return StepResult{IsFinal: true, Text: step.FinalText}, nil
	}

	resolved, err := e.resolveInput(step, history)
	if err != nil {
		return StepResult{}, fmt.Errorf("劇本第 %d 步(工具 %s)解析參數失敗: %w", idx+1, step.ToolName, err)
	}

	if e.onStep != nil {
		e.onStep(idx, step, resolved)
	}

	return StepResult{
		ToolName: step.ToolName,
		ToolID:   fmt.Sprintf("mockllm-call-%d", idx+1),
		Input:    resolved,
	}, nil
}

func (e *Engine) resolveInput(step Step, history []ToolResult) (map[string]interface{}, error) {
	out := make(map[string]interface{}, len(step.Input))
	for k, v := range step.Input {
		out[k] = v
	}
	if step.ExtractFrom == nil {
		return out, nil
	}
	value, err := extractFromHistory(history, *step.ExtractFrom)
	if err != nil {
		return nil, err
	}
	for k, v := range out {
		if s, ok := v.(string); ok && s == "$1" {
			out[k] = value
		}
	}
	return out, nil
}

// extractFromHistory 依 rule.Occurrence 找出符合 FromToolName 的第 N 次結果
// (Occurrence<=0 時取「最近一次」,即由新到舊掃到的第一筆),對其 Text 套用
// rule.Pattern,回傳第 1 個 capture group。
func extractFromHistory(history []ToolResult, rule ExtractRule) (string, error) {
	if rule.Occurrence > 0 {
		seen := 0
		for _, r := range history {
			if r.ToolName != rule.FromToolName {
				continue
			}
			seen++
			if seen != rule.Occurrence {
				continue
			}
			m := rule.Pattern.FindStringSubmatch(r.Text)
			if len(m) < 2 {
				return "", fmt.Errorf("第 %d 次 %s 的結果文字未配對正則 %s(文字=%q)", rule.Occurrence, rule.FromToolName, rule.Pattern.String(), r.Text)
			}
			return m[1], nil
		}
		return "", fmt.Errorf("history 裡找不到第 %d 次 %s 的執行結果", rule.Occurrence, rule.FromToolName)
	}

	for i := len(history) - 1; i >= 0; i-- {
		r := history[i]
		if r.ToolName != rule.FromToolName {
			continue
		}
		m := rule.Pattern.FindStringSubmatch(r.Text)
		if len(m) < 2 {
			continue
		}
		return m[1], nil
	}
	return "", fmt.Errorf("history 裡找不到符合的 %s 執行結果(或其文字未配對正則 %s)", rule.FromToolName, rule.Pattern.String())
}
