package mockllm

import (
	"testing"
)

// TestEngine_ExtractsRealEntryID_NotStaticPlaceholder 是這個套件存在的核心理由的
// 回歸測試:驗證 Engine.Next 真的會從呼叫端傳入的 history(模擬 want 傳來的、
// 貨真價實的前一輪工具執行結果)動態撈出 entryID,而不是把 "$1" 這個佔位符原封
// 不動送出去——若這個測試失敗,代表整個「動態 mock provider」的核心承諾破功,
// 退化回跟 want 內建 provider.MockProvider 一樣的純線性重播。
func TestEngine_ExtractsRealEntryID_NotStaticPlaceholder(t *testing.T) {
	steps := []Step{
		{ToolName: "entry_add", Input: map[string]interface{}{"title": "測試"}},
		{
			ToolName: "entry_update",
			Input:    map[string]interface{}{"entryID": "$1", "title": "改過的標題"},
			ExtractFrom: &ExtractRule{
				FromToolName: "entry_add",
				Pattern:      EntryIDPattern,
			},
		},
	}
	engine := NewEngine(steps, nil)

	// 第一步:不需要歷史,直接送出。
	r1, err := engine.Next(nil)
	if err != nil {
		t.Fatalf("第 1 步不應該出錯: %v", err)
	}
	if r1.ToolName != "entry_add" {
		t.Fatalf("第 1 步應該是 entry_add,實際是 %s", r1.ToolName)
	}

	// 模擬 want 執行完 entry_add 後,真實寫入 DB 產生的 entryID 透過
	// resultMsg(格式對齊 entry_add.go 的 "Recorded (entryID=%s): %s %s")
	// 回傳,呼叫端(cmd/mockllm 的 HTTP handler)解析成 ToolResult 傳入。
	const realEntryID = "ent_deadbeef12"
	history := []ToolResult{
		{ToolName: "entry_add", Text: "Recorded (entryID=" + realEntryID + "): tomorrow 14:00 測試"},
	}

	r2, err := engine.Next(history)
	if err != nil {
		t.Fatalf("第 2 步不應該出錯: %v", err)
	}
	if r2.ToolName != "entry_update" {
		t.Fatalf("第 2 步應該是 entry_update,實際是 %s", r2.ToolName)
	}
	got, _ := r2.Input["entryID"].(string)
	if got != realEntryID {
		t.Fatalf("entryID 應該被動態替換成真實值 %q,實際是 %q(若等於 \"$1\" 代表佔位符沒被替換,退化成靜態腳本)", realEntryID, got)
	}
	if r2.Input["title"] != "改過的標題" {
		t.Fatalf("非動態欄位(title)應保持固定值不變,實際是 %v", r2.Input["title"])
	}
}

// TestEngine_OccurrenceDistinguishesFirstFromMostRecent 驗證 ExtractRule.Occurrence
// 真的能區分「第一次」與「最近一次」——這是本次端到端測試劇本第 4 步
// (entry_delete 要刪第 1 步那筆,而非第 3 步剛新增的第二筆)賴以正確運作的關鍵
// 邏輯。若這個機制壞掉,劇本會誤刪剛新增的那筆而非原本要刪的那筆,且不會產生
// 明顯的錯誤(兩筆都是合法的 entryID),只會在檢查「哪一筆真的被刪了」時才會
// 發現——這正是使用者原始要求裡特別點名要避免的那種「靜默寫錯腳本」情境。
func TestEngine_OccurrenceDistinguishesFirstFromMostRecent(t *testing.T) {
	history := []ToolResult{
		{ToolName: "entry_add", Text: "Recorded (entryID=ent_first000): tomorrow 14:00 第一筆"},
		{ToolName: "entry_update", Text: "Entry ent_first000 updated"},
		{ToolName: "entry_add", Text: "Recorded (entryID=ent_second00): in 2 days 10:00 第二筆"},
	}

	t.Run("Occurrence未設時取最近一次", func(t *testing.T) {
		steps := []Step{{
			ToolName: "entry_delete",
			Input:    map[string]interface{}{"entryID": "$1"},
			ExtractFrom: &ExtractRule{
				FromToolName: "entry_add",
				Pattern:      EntryIDPattern,
			},
		}}
		engine := NewEngine(steps, nil)
		r, err := engine.Next(history)
		if err != nil {
			t.Fatalf("不應該出錯: %v", err)
		}
		if got := r.Input["entryID"]; got != "ent_second00" {
			t.Fatalf("未設 Occurrence 應取最近一次(第二筆 ent_second00),實際是 %v", got)
		}
	})

	t.Run("Occurrence=1時取第一次", func(t *testing.T) {
		steps := []Step{{
			ToolName: "entry_delete",
			Input:    map[string]interface{}{"entryID": "$1"},
			ExtractFrom: &ExtractRule{
				FromToolName: "entry_add",
				Pattern:      EntryIDPattern,
				Occurrence:   1,
			},
		}}
		engine := NewEngine(steps, nil)
		r, err := engine.Next(history)
		if err != nil {
			t.Fatalf("不應該出錯: %v", err)
		}
		if got := r.Input["entryID"]; got != "ent_first000" {
			t.Fatalf("Occurrence=1 應取第一次(第一筆 ent_first000),實際是 %v(若等於 ent_second00 代表誤刪成第二筆,對應本次端到端測試劇本會誤刪剛新增的淺草寺而非原本要刪的東京晴空塔)", got)
		}
	})
}

// TestEngine_FinalTextEndsScript 驗證帶 FinalText 的步驟回傳 IsFinal,且劇本
// 用完後(cursor 超出 steps 長度)回退到空文字,行為對齊 want 內建
// provider.MockProvider 腳本用完後的回退方式(避免 want 的 Agent.Run 卡在
// maxRounds=50 才結束,而是像正常 LLM 一樣「這輪沒有 tool_use」就自然收尾)。
func TestEngine_FinalTextEndsScript(t *testing.T) {
	steps := []Step{
		{FinalText: "收尾文字"},
	}
	engine := NewEngine(steps, nil)

	r, err := engine.Next(nil)
	if err != nil {
		t.Fatalf("不應該出錯: %v", err)
	}
	if !r.IsFinal || r.Text != "收尾文字" {
		t.Fatalf("應該回傳 IsFinal=true 且 Text=收尾文字,實際 IsFinal=%v Text=%q", r.IsFinal, r.Text)
	}
	if !engine.Done() {
		t.Fatalf("跑完唯一一步後 Done() 應該回傳 true")
	}

	r2, err := engine.Next(nil)
	if err != nil {
		t.Fatalf("腳本用完後呼叫 Next 不應該出錯: %v", err)
	}
	if !r2.IsFinal || r2.Text != "" {
		t.Fatalf("腳本用完後應回退成空文字收尾,實際 IsFinal=%v Text=%q", r2.IsFinal, r2.Text)
	}
}

// TestEngine_ExtractFailure_ReturnsError 驗證擷取規則找不到符合的歷史紀錄時,
// Engine.Next 會回傳明確的 error,而不是靜默送出空字串當 entryID——避免測試
// 腳本寫錯時,錯誤被吞掉、以一個看起來「成功」但實際上是空 entryID 呼叫
// entry_update/entry_delete 的方式失敗(那類失敗會在更下游、更難追查的地方
// 才顯現,例如 entry_update 的 normalizeEntryID 會把空字串補成 "ent_" 這個
// 不存在的 ID,導致 ValidateInput 回傳「找不到」的錯誤,但那時已經很難追溯
// 回「其實是 mock 腳本的擷取規則沒配對到」這個根因)。
func TestEngine_ExtractFailure_ReturnsError(t *testing.T) {
	steps := []Step{{
		ToolName: "entry_update",
		Input:    map[string]interface{}{"entryID": "$1"},
		ExtractFrom: &ExtractRule{
			FromToolName: "entry_add",
			Pattern:      EntryIDPattern,
		},
	}}
	engine := NewEngine(steps, nil)

	_, err := engine.Next(nil) // 空 history:找不到任何 entry_add 的結果。
	if err == nil {
		t.Fatalf("history 裡沒有符合的工具結果時,應該回傳 error,而不是靜默成功")
	}
}
