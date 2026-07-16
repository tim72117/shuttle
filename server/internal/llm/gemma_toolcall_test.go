package llm

import (
	"testing"
)

// 真實樣本(取自 server/logs/response/..._R7.json):
//
//	present_entries{allDay:true, end:"", title:"生日", start:"2026-06-25"}
func TestParseGemmaToolCalls_RealSample(t *testing.T) {
	raw := `<|tool_call>call:present_entries{allDay:true,end:<|"|><|"|>,title:<|"|>生日<|"|>,start:<|"|>2026-06-25<|"|>}<tool_call|>`

	calls, remaining := ParseGemmaToolCalls(raw, nil)
	if len(calls) != 1 {
		t.Fatalf("應解析出 1 個 tool-call,得到 %d", len(calls))
	}
	if remaining != "" {
		t.Errorf("剩餘文字應為空,得到 %q", remaining)
	}
	tu := calls[0].ToolUse
	if tu == nil || tu.Name != "present_entries" {
		t.Fatalf("工具名錯誤: %+v", tu)
	}
	if got := tu.Input.GetBool("allDay"); got != true {
		t.Errorf("allDay 應為 true,得到 %v", got)
	}
	if got := tu.Input.GetString("end"); got != "" {
		t.Errorf("end 應為空字串,得到 %q", got)
	}
	if got := tu.Input.GetString("title"); got != "生日" {
		t.Errorf("title 應為 生日,得到 %q", got)
	}
	if got := tu.Input.GetString("start"); got != "2026-06-25" {
		t.Errorf("start 應為 2026-06-25,得到 %q", got)
	}
}

func TestParseGemmaToolCalls_NoToolCall(t *testing.T) {
	text := "這只是一段普通回答,沒有工具呼叫。"
	calls, remaining := ParseGemmaToolCalls(text, nil)
	if calls != nil {
		t.Errorf("不應解析出 tool-call,得到 %d", len(calls))
	}
	if remaining != text {
		t.Errorf("剩餘文字應為原文,得到 %q", remaining)
	}
}

// 前後夾雜一般文字應被保留為 remaining。
func TestParseGemmaToolCalls_SurroundingText(t *testing.T) {
	raw := `好的,我幫你記下。<|tool_call>call:record_entry{title:<|"|>開會<|"|>,allDay:false}<tool_call|>已完成。`
	calls, remaining := ParseGemmaToolCalls(raw, nil)
	if len(calls) != 1 {
		t.Fatalf("應解析出 1 個 tool-call,得到 %d", len(calls))
	}
	if remaining != "好的,我幫你記下。已完成。" {
		t.Errorf("剩餘文字錯誤,得到 %q", remaining)
	}
	if got := calls[0].ToolUse.Input.GetBool("allDay"); got != false {
		t.Errorf("allDay 應為 false,得到 %v", got)
	}
}

// 多個 tool-call 連續出現。
func TestParseGemmaToolCalls_Multiple(t *testing.T) {
	raw := `<|tool_call>call:present_entries{title:<|"|>A<|"|>}<tool_call|><|tool_call>call:present_entries{title:<|"|>B<|"|>}<tool_call|>`
	calls, _ := ParseGemmaToolCalls(raw, nil)
	if len(calls) != 2 {
		t.Fatalf("應解析出 2 個 tool-call,得到 %d", len(calls))
	}
	if calls[0].ToolUse.Input.GetString("title") != "A" ||
		calls[1].ToolUse.Input.GetString("title") != "B" {
		t.Errorf("title 解析錯誤: %v / %v",
			calls[0].ToolUse.Input.GetString("title"),
			calls[1].ToolUse.Input.GetString("title"))
	}
}

// 串流截斷:缺閉標記與右括號,應盡力解析既有片段。
func TestParseGemmaToolCalls_Truncated(t *testing.T) {
	raw := `<|tool_call>call:present_entries{allDay:true,title:<|"|>吃飯<|"|>,start:<|"|>2026-06-24`
	calls, _ := ParseGemmaToolCalls(raw, nil)
	if len(calls) != 1 {
		t.Fatalf("截斷情況仍應盡力解析出 1 個,得到 %d", len(calls))
	}
	tu := calls[0].ToolUse
	if tu.Input.GetString("title") != "吃飯" {
		t.Errorf("title 應為 吃飯,得到 %q", tu.Input.GetString("title"))
	}
	// start 的字串未閉合 → 容錯吃到結尾。
	if tu.Input.GetString("start") != "2026-06-24" {
		t.Errorf("start 截斷容錯應為 2026-06-24,得到 %q", tu.Input.GetString("start"))
	}
}

// 數字值應轉成 float64(與 json.Unmarshal 一致),供 GetInt 取用。
func TestParseGemmaToolCalls_NumericValue(t *testing.T) {
	raw := `<|tool_call>call:set_count{n:42,ratio:0.5}<tool_call|>`
	calls, _ := ParseGemmaToolCalls(raw, nil)
	if len(calls) != 1 {
		t.Fatalf("應解析出 1 個,得到 %d", len(calls))
	}
	if got := calls[0].ToolUse.Input.GetInt("n"); got != 42 {
		t.Errorf("n 應為 42,得到 %d", got)
	}
	if got, ok := calls[0].ToolUse.Input["ratio"].(float64); !ok || got != 0.5 {
		t.Errorf("ratio 應為 float64 0.5,得到 %v", calls[0].ToolUse.Input["ratio"])
	}
}

// 字串值內含逗號與冒號,不應被誤切。
func TestParseGemmaToolCalls_DelimitersInsideString(t *testing.T) {
	raw := `<|tool_call>call:note{text:<|"|>09:00, 與 Bob 開會<|"|>}<tool_call|>`
	calls, _ := ParseGemmaToolCalls(raw, nil)
	if len(calls) != 1 {
		t.Fatalf("應解析出 1 個,得到 %d", len(calls))
	}
	if got := calls[0].ToolUse.Input.GetString("text"); got != "09:00, 與 Bob 開會" {
		t.Errorf("含逗號冒號的字串解析錯誤,得到 %q", got)
	}
}
