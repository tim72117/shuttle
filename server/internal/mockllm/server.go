package mockllm

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
)

// Server 是一個實作 OpenAI 相容 POST /v1/chat/completions(SSE 串流)協定的
// HTTP 伺服器,足以讓 want 的 VLLMProvider(internal/provider/vllm.go)把它當成
// 一台真的 vLLM 伺服器連線。背後的「推論邏輯」由 Engine 決定(見 script.go)。
//
// 使用方式:設定 AI_PROVIDER=vllm 與 VLLM_BASE_URL=http://127.0.0.1:<Server 監聽埠>,
// 讓 shuttle 用完全未修改的 want_analyzer.go NewWant() 啟動路徑連過來——本伺服器
// 在 want 眼中是一台正常的 vLLM,只是背後真正決定「下一步做什麼」的是 Engine 這份
// 動態劇本,而非真的語言模型推論。
type Server struct {
	mu     sync.Mutex
	engine *Engine
}

// NewServer 用一個 Engine 建立 Server。單一 Engine 實例服務所有請求
// (對齊 want 目前 GlobalEngine 是 process 單例、WantPool.For 永遠回傳同一個
// 共用 orchestrator 的現況——見 server/internal/llm/want_pool.go 的說明,
// 一個 process 內任何時刻只有一輪推論在跑,不需要 per-request 狀態隔離)。
func NewServer(engine *Engine) *Server {
	return &Server{engine: engine}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	// VLLMProvider.GetContextWindowSize 會打 GET /v1/models(目前 want_analyzer.go
	// 的呼叫路徑不會呼叫到這個方法,但保留一個最簡回應以防萬一被間接觸發)。
	mux.HandleFunc("GET /v1/models", s.handleModels)
	return mux
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": []map[string]any{{"id": "mockllm", "max_model_len": 16384}},
	})
}

// openAIChatMessage 是進來的請求裡 messages 陣列一個元素的寬鬆解析形狀。
// 同時涵蓋三種角色:
//   - assistant 帶 tool_calls(我們自己上一輪送出的,原樣回聲回來,不需要處理)
//   - tool(role=="tool"):真實工具執行完的結果,content 為字串,
//     tool_call_id 對應到我們稍早送出的 tool_calls[].id。
//   - user/system:純文字。
type openAIChatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type openAIChatRequest struct {
	Messages []openAIChatMessage `json:"messages"`
}

// handleChatCompletions 是核心邏輯:解析請求歷史 → 從中重建「目前為止各工具的
// 執行結果」→ 交給 Engine.Next 決定下一步 → 用 SSE 串流吐回 want 期待的格式。
//
// 之所以能從請求重建工具執行結果,是因為 want 的 VLLMProvider.prepareRequest
// (已讀原始碼確認)會把「我們稍早送出的 tool_calls」與「want 執行完那些工具後的
// 真實結果」都序列化進同一個 messages 陣列:assistant 訊息帶 tool_calls(含我們
// 稍早指定的 id 與工具名),緊接著的 tool 訊息帶 tool_call_id(配對回那個 id)
// 與 content(執行結果文字,對應 entry_add.go 的 resultMsg 等)。
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var req openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}

	// 建 tool_call_id → 工具名 的對照表(來自我們自己稍早送出、want 回聲回來的
	// assistant.tool_calls),再依此把 role=="tool" 的訊息轉成 []ToolResult。
	idToTool := map[string]string{}
	for _, m := range req.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				idToTool[tc.ID] = tc.Function.Name
			}
		}
	}

	var history []ToolResult
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		toolName := idToTool[m.ToolCallID]
		history = append(history, ToolResult{ToolName: toolName, Text: decodeContentText(m.Content)})
	}

	result, err := s.engine.Next(history)
	if err != nil {
		log.Printf("[mockllm] Engine.Next 失敗: %v", err)
		http.Error(w, fmt.Sprintf("mockllm script error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	writeChunk := func(chunk map[string]any) {
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	if result.IsFinal {
		writeChunk(map[string]any{
			"id": "mockllm-chunk",
			"choices": []map[string]any{{
				"delta":         map[string]any{"content": result.Text},
				"finish_reason": nil,
			}},
		})
	} else {
		argsJSON, _ := json.Marshal(result.Input)
		writeChunk(map[string]any{
			"id": "mockllm-chunk",
			"choices": []map[string]any{{
				"delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": 0,
						"id":    result.ToolID,
						"function": map[string]any{
							"name":      result.ToolName,
							"arguments": string(argsJSON),
						},
					}},
				},
				"finish_reason": nil,
			}},
		})
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// decodeContentText 把 OpenAI content 欄位(可能是純字串,也可能是
// [{"type":"text","text":"..."}] 這種多模態陣列——want 的 MarshalOpenAIToolResult
// 兩種都可能產生,已讀原始碼確認)攤平成單一字串。
func decodeContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}
