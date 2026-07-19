// Command mockllm 是一個獨立的測試/開發輔助工具:啟動一台實作 OpenAI 相容
// POST /v1/chat/completions(SSE 串流)協定的假 LLM 伺服器,讓 shuttle 的 want
// orchestrator(透過 AI_PROVIDER=vllm + VLLM_BASE_URL=http://127.0.0.1:<本工具監聽埠>)
// 把它當成一台真的 vLLM 伺服器連線——但背後不打真正的語言模型,而是依序執行一份
// 寫死在本檔案的動態劇本(entry_add → entry_update → entry_add → entry_delete),
// 且第 2、4 步會從 want 傳來的真實請求歷史裡動態撈出第 1 步真正寫入資料庫後才會
// 產生的 entryID(格式 ent_<hex>),不是重播寫死的字串。
//
// # 用途:端到端測試「LLM 推論安排整段行程,過程中新增/刪除/修改,前端即時更新」
//
// 這是為了做一次快速的端到端驗證:只 mock LLM 這一層,其餘(真實瀏覽器、真實
// WebSocket、真實後端 process、真實 want orchestrator、真實 entry_add/entry_update/
// entry_delete 工具執行、真實 DB 寫入、真實 entries_updated WS 廣播、真實前端
// MultiTrackTimeline 渲染)全部是真的。詳見 server/internal/mockllm 套件開頭的
// 技術說明(為什麼是這個做法,而不是直接把 provider.AIProvider 實例注入 want
// orchestrator——已確認 want v0.0.2 沒有這樣的公開入口)。
//
// # 這不是 shuttle 產品本身要對外提供的功能
//
// 跟 cmd/agentbench 屬於同一類:完全獨立的一次性/開發用 cmd,不會被 cmd/server
// 的正式啟動路徑引用,production main.go 完全不知道這個套件的存在。
//
// # 執行方式
//
//	go run ./cmd/mockllm -addr :9999
//
// 監聽位址預設 :9999(與 cmd/server 的 :8080、cmd/agentbench 的 :8090 錯開),
// 可用 -addr flag 覆寫。啟動後,另開一個 terminal 啟動 shuttle server 並指向它:
//
//	AI_PROVIDER=vllm VLLM_BASE_URL=http://127.0.0.1:9999 \
//	  go run ./cmd/server -db /tmp/shuttle_e2e_test.db -llm want
//
// 完整重跑腳本見 server/scripts/run_e2e_mock_llm_test.sh(啟動 mockllm + shuttle
// server + 前端 dev server 三個 process,並印出後續驗證步驟)。
//
// # 劇本內容
//
// 四輪操作,對齊使用者要在前端觸發的單一句「幫我安排一趟東京兩天行程」:
//  1. entry_add:「明天下午兩點去東京晴空塔」
//  2. entry_update:把第 1 步剛寫入的那筆(用第 1 步真實回傳的 entryID)標題改成
//     「東京晴空塔(展望台預約 14:30)」、時間改成 14:30
//  3. entry_add:「後天早上十點去淺草寺」(第二筆行程,證明「安排整段行程」
//     至少涵蓋兩筆)
//  4. entry_delete:刪除第 1 步那筆(同樣用第 1 步真實回傳的 entryID,證明第 2
//     步之後那個 entryID 仍然可靠地被本劇本引用、不是巧合)
//  5. 純文字收尾,結束這輪推論(want 的 Agent.Run 看到「本輪沒有 tool_use」
//     就會結束,見 want/internal/query.go)
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/tim72117/tripace/internal/mockllm"
)

func main() {
	addr := flag.String("addr", ":9999", "HTTP 監聽位址(預設 :9999,避開 cmd/server 的 :8080、cmd/agentbench 的 :8090)")
	flag.Parse()
	if v := os.Getenv("MOCKLLM_ADDR"); v != "" {
		*addr = v
	}

	engine := mockllm.NewEngine(tokyoTripScript(), logStep)
	srv := mockllm.NewServer(engine)

	log.Printf("[mockllm] 監聽 %s(假 LLM,OpenAI 相容 /v1/chat/completions,供 AI_PROVIDER=vllm 連接)", *addr)
	log.Printf("[mockllm] 劇本:entry_add → entry_update → entry_add → entry_delete → 結束")
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatalf("[mockllm] server: %v", err)
	}
}

// logStep 是 Engine 的觀測鉤子,每送出一步就印一行,方便重跑測試時肉眼確認
// 四輪操作真的依序被呼叫、且動態撈到的 entryID 看起來合理(不是空字串)。
func logStep(idx int, step mockllm.Step, resolved map[string]interface{}) {
	if step.FinalText != "" {
		log.Printf("[mockllm] 第 %d 步:純文字收尾 %q", idx+1, step.FinalText)
		return
	}
	log.Printf("[mockllm] 第 %d 步:呼叫 %s(參數=%v)", idx+1, step.ToolName, resolved)
}

// tokyoTripScript 是本次端到端測試用的固定劇本。
func tokyoTripScript() []mockllm.Step {
	return []mockllm.Step{
		// 第 1 步:新增第一筆行程。entryID 由 shuttle 端真實寫入 DB 後才產生
		// (格式 "ent_" + 隨機 hex,見 cmd/server/main.go 的 wanttools.BindSink),
		// 本步驟本身不需要引用任何動態值。
		{
			ToolName: "entry_add",
			Input: map[string]interface{}{
				"title":     "東京晴空塔",
				"start":     "tomorrow",
				"startTime": "14:00",
			},
		},
		// 第 2 步:修改第 1 步剛新增的那筆。entryID 透過 ExtractFrom 從 history
		// 裡「最近一次 entry_add 的執行結果」動態撈出(此時 history 裡只有第 1 步
		// 那一筆 entry_add 結果,「最近一次」即「第一次」,語意上等價,但走的是
		// 通用的「最近一次」規則,不是寫死 Occurrence:1)。
		{
			ToolName: "entry_update",
			Input: map[string]interface{}{
				"entryID":   "$1",
				"title":     "東京晴空塔(展望台預約 14:30)",
				"startTime": "14:30",
			},
			ExtractFrom: &mockllm.ExtractRule{
				FromToolName: "entry_add",
				Pattern:      mockllm.EntryIDPattern,
			},
		},
		// 第 3 步:新增第二筆行程(不依賴任何動態值)。
		{
			ToolName: "entry_add",
			Input: map[string]interface{}{
				"title":     "淺草寺",
				"start":     "in 2 days",
				"startTime": "10:00",
			},
		},
		// 第 4 步:刪除「第一筆」(第 1 步那筆),不是「最近一筆」——此時 history
		// 裡已經有兩次 entry_add 的結果(第 1、3 步),必須明確指定
		// Occurrence:1(由舊到新數第一次出現)才能撈到正確的、不是第 3 步剛
		// 新增那筆的 entryID。這是本劇本裡唯一「最近一次」規則不夠用、
		// 必須用 Occurrence 明確指定第幾次的步驟,刻意保留以驗證 ExtractRule
		// 的 Occurrence 機制真的有作用(若寫錯成撈到「最近一次」,會誤刪第 3
		// 步剛新增的淺草寺那筆,Playwright 驗證階段會清楚看到時間軸上留下
		// 東京晴空塔、淺草寺消失,與劇本意圖相反,測試會明確失敗而非誤報成功)。
		{
			ToolName: "entry_delete",
			Input: map[string]interface{}{
				"entryID": "$1",
			},
			ExtractFrom: &mockllm.ExtractRule{
				FromToolName: "entry_add",
				Pattern:      mockllm.EntryIDPattern,
				Occurrence:   1,
			},
		},
		// 第 5 步:純文字收尾,結束整輪推論。
		{
			FinalText: "已經幫你安排好東京兩天行程:調整了晴空塔的預約時間,並新增了淺草寺;原本的晴空塔那筆行程改成透過展望台預約的新時段記錄,舊的那筆已經移除重複。",
		},
	}
}
