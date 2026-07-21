package wanttools

import (
	"sync"

	"github.com/tim72117/want/types"
)

// RecordedEntry 是 record_entry 工具解析出的一筆條目。
type RecordedEntry struct {
	Title     string
	Start     string // 'YYYY-MM-DD'
	StartTime string // 'HH:MM';空=全日
	End       string // 'YYYY-MM-DD'
	EndTime   string // 'HH:MM'
	Kind      string // 條目類型(如 "stay");空=未分類
}

// EntrySink 同步把一筆條目寫入 store(entry 為主體,獨立寫入),
// 回傳新建 entry 的 ID 供之後與來源 message 建立關聯。
// server 啟動時用 BindSink 注入。
type EntrySink func(channelID string, e RecordedEntry) (entryID string, err error)

// PresentedEntry 是 present_entries 工具輸出的一筆「要展示給使用者」的條目。
type PresentedEntry struct {
	Title     string `json:"title"`
	Start     string `json:"start"`
	StartTime string `json:"startTime"`
	End       string `json:"end"`
	EndTime   string `json:"endTime"`
}

// RecommendedPlace 是 recommend_nearby 工具查到的一筆候選景點,供呼叫端在
// agent 這輪推論結束後隨 AssistResult 一併回給前端,掛在觸發它的那則訊息底下顯示
// (與 PresentedEntry/presented/addPresented/Presented() 同一套「累積本次流程
// 結果」機制,對稱 present_entries 的既有模式;取代先前用 NotifyRecommendedPlaces
// 異步 WS 廣播、與訊息脫鉤的做法)。
type RecommendedPlace struct {
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	PrimaryType string  `json:"primaryType"`
}

// NotifyFn 廣播 entries_updated 事件給前端(server 啟動時用 BindNotify 注入)。
type NotifyFn func(channelID string)

// EntryUpdatingFn 廣播 entry_updating 事件(帶 entryID)給前端,
// 讓對應條目卡片在工具更新期間顯示「更新中」動畫(server 啟動時用 BindEntryUpdating 注入)。
type EntryUpdatingFn func(channelID, entryID string)

// AskUserFn 廣播 ask_user 事件給前端,讓前端開啟對應 UI(如日期選擇器)
// 請使用者補上缺失資訊(server 啟動時用 BindAskUser 注入)。
type AskUserFn func(channelID, askType, prompt string)

// AskChoiceOption 是 ask_choice 工具的一個選項:主標題 + 一行描述(可選)。
type AskChoiceOption struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// AskChoiceFn 廣播 ask_choice 事件給前端,讓前端開啟選單 UI 請使用者從
// options 中選一個(server 啟動時用 BindAskChoice 注入)。
// options 用 []map[string]any 而非具名型別,避免 api 套件為了此簽章反向
// 依賴 wanttools 的 AskChoiceOption。
type AskChoiceFn func(channelID, prompt string, options []map[string]any)

// TaskCreatedFn 廣播 task_created 事件(帶 taskID/date/text/kind)給前端,
// 讓前端在該日期下插入一張標示動作(新增/更新)的佔位卡(server 啟動時用 BindTaskCreated 注入)。
type TaskCreatedFn func(channelID string, taskID int, date, text, kind string)

// TaskEntryReadyFn 廣播 task_entry_ready 事件(帶 taskID/entryID)給前端,
// 讓前端把對應的佔位卡直接替換成正式條目卡(server 啟動時用 BindTaskEntryReady 注入)。
type TaskEntryReadyFn func(channelID string, taskID int, entryID string)

// TripEntryPayload 是 entry_query 查到、要推播給前端旅程清單表格的一筆條目,
// 欄位對齊前端 TripEntry(web/src/clienttools/tripEntryTools.ts)與
// server/tools/clienttools.yaml 裡 trip_entry_add/trip_entry_list 的欄位
// 命名(title/date/time/note),而非 model.Entry 的 start/startTime 命名——
// 這樣前端收到後不需要再做一次欄位名稱轉換,可直接當 TripEntry 使用。
type TripEntryPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Date  string `json:"date"`
	Time  string `json:"time"`
	Note  string `json:"note"`
}

// EntriesLoadedFn 廣播 entries_loaded 事件(帶 entry_query 查到的條目清單)給
// 前端,讓前端合併進旅程清單表格供使用者查看/編輯(server 啟動時用
// BindEntriesLoaded 注入)。沿用 AskChoiceFn 的風格,用 []map[string]any 而非
// TripEntryPayload,避免 api 套件為了此簽章反向依賴 wanttools;
// NotifyEntriesLoaded 負責把 []TripEntryPayload 轉成這個形狀。
type EntriesLoadedFn func(channelID string, entries []map[string]any)

var (
	// recordMu 序列化整個「記錄一則訊息」的流程,確保 RecordLock 保護的計數/清單不交錯。
	recordMu       sync.Mutex
	sink           EntrySink
	notifyFn       NotifyFn
	entryUpdating  EntryUpdatingFn
	askUser        AskUserFn
	askChoice      AskChoiceFn
	taskCreated    TaskCreatedFn
	taskEntryReady TaskEntryReadyFn
	entriesLoaded  EntriesLoadedFn
	// emitCount 記本次 RecordLock 流程內 record_entry 被觸發的次數,
	// 供呼叫端判斷 agent 究竟「記錄了」還是「只回答」。
	emitCount int
	// emittedIDs 收集本次流程內 emit 成功寫入的 entry ID,
	// 供呼叫端在來源 message 寫入後,逐一建立 entry↔message 關聯。
	emittedIDs []string
	// presented 收集本次流程內 present_entries 工具輸出的條目,
	// 由呼叫端取出回傳前端用列表元件顯示。
	presented []PresentedEntry
	// recommendedPlaces 收集本次流程內 recommend_nearby 工具查到的候選景點,
	// 由呼叫端取出、隨 AssistResult 回傳前端掛在觸發它的那則訊息底下顯示
	// (與 presented 同一套「累積本次流程結果」機制,取代先前的異步 WS 廣播)。
	recommendedPlaces []RecommendedPlace
)

// BindSink 注入條目持久化實作(server 啟動時呼叫)。
func BindSink(fn EntrySink) { sink = fn }

// BindNotify 注入廣播函式(server 啟動時呼叫),讓工具更新後能通知前端刷新。
func BindNotify(fn NotifyFn) { notifyFn = fn }

// Notify 廣播 entries_updated,供工具呼叫。
func Notify(channelID string) {
	if notifyFn != nil {
		notifyFn(channelID)
	}
}

// BindEntryUpdating 注入 entry_updating 廣播函式(server 啟動時呼叫)。
func BindEntryUpdating(fn EntryUpdatingFn) { entryUpdating = fn }

// NotifyEntryUpdating 廣播 entry_updating(帶 entryID),供工具在開始更新前呼叫,
// 讓前端立即把對應條目卡片切成「更新中」狀態。
func NotifyEntryUpdating(channelID, entryID string) {
	if entryUpdating != nil {
		entryUpdating(channelID, entryID)
	}
}

// BindAskUser 注入 ask_user 廣播函式(server 啟動時呼叫)。
func BindAskUser(fn AskUserFn) { askUser = fn }

// NotifyAskUser 廣播 ask_user(帶 askType/prompt),供 ask_user 工具呼叫,
// 讓前端開啟對應 UI 請使用者補上缺失資訊。
func NotifyAskUser(channelID, askType, prompt string) {
	if askUser != nil {
		askUser(channelID, askType, prompt)
	}
}

// BindAskChoice 注入 ask_choice 廣播函式(server 啟動時呼叫)。
func BindAskChoice(fn AskChoiceFn) { askChoice = fn }

// NotifyAskChoice 廣播 ask_choice(帶 prompt/options),供 ask_choice 工具呼叫,
// 讓前端開啟選單 UI 請使用者從 options 中選一個。
func NotifyAskChoice(channelID, prompt string, options []AskChoiceOption) {
	if askChoice != nil {
		out := make([]map[string]any, len(options))
		for i, o := range options {
			out[i] = map[string]any{"title": o.Title, "description": o.Description}
		}
		askChoice(channelID, prompt, out)
	}
}

// BindTaskCreated 注入 task_created 廣播函式(server 啟動時呼叫)。
func BindTaskCreated(fn TaskCreatedFn) { taskCreated = fn }

// NotifyTaskCreated 廣播 task_created(帶 taskID/date/text/kind),供 task_plan 的
// create 呼叫,讓前端在該日期下插入一張標示動作(新增/更新)的佔位卡。
func NotifyTaskCreated(channelID string, taskID int, date, text, kind string) {
	if taskCreated != nil {
		taskCreated(channelID, taskID, date, text, kind)
	}
}

// BindTaskEntryReady 注入 task_entry_ready 廣播函式(server 啟動時呼叫)。
func BindTaskEntryReady(fn TaskEntryReadyFn) { taskEntryReady = fn }

// NotifyTaskEntryReady 廣播 task_entry_ready(帶 taskID/entryID),供 entry_add
// 在完成且帶有 taskID 時呼叫,讓前端把對應的佔位卡直接替換成正式條目卡。
func NotifyTaskEntryReady(channelID string, taskID int, entryID string) {
	if taskEntryReady != nil {
		taskEntryReady(channelID, taskID, entryID)
	}
}

// BindEntriesLoaded 注入 entries_loaded 廣播函式(server 啟動時呼叫)。
func BindEntriesLoaded(fn EntriesLoadedFn) { entriesLoaded = fn }

// NotifyEntriesLoaded 廣播 entries_loaded(帶轉換成 TripEntryPayload 的條目清單),
// 供 entry_query 工具呼叫,讓前端把查到的條目合併進旅程清單表格供使用者查看/編輯。
// entries 為空陣列時仍會廣播(讓前端知道「這次查詢查無結果」),由前端決定如何呈現。
func NotifyEntriesLoaded(channelID string, entries []TripEntryPayload) {
	if entriesLoaded != nil {
		out := make([]map[string]any, len(entries))
		for i, e := range entries {
			out[i] = map[string]any{"id": e.ID, "title": e.Title, "date": e.Date, "time": e.Time, "note": e.Note}
		}
		entriesLoaded(channelID, out)
	}
}

// ChannelFrom 從 ctx 的 SessionEnvs 讀 channelID:這份資料綁在本次呼叫的
// ToolUseContext 上(由 want_analyzer.go 於 Submit 前透過 orch.SetSessionEnvs 寫入),
// 不經過任何套件級全域變數,也不會被組進送給 LLM 的 prompt。
// ctx 為 nil 或未設定時回空字串(呼叫端應自行判斷是否視為錯誤)。
func ChannelFrom(ctx types.ToolContext) string {
	if ctx == nil {
		return ""
	}
	envs := ctx.GetSessionEnvs()
	if envs == nil {
		return ""
	}
	return envs["channelID"]
}

// RecordLock / RecordUnlock 包住一次完整的記錄流程(設定 context → 跑 agent → 清除)。
// RecordLock 同時把本次的 emit 計數、已寫入 ID、展示條目、推薦景點歸零。
func RecordLock() {
	recordMu.Lock()
	emitCount = 0
	emittedIDs = nil
	presented = nil
	recommendedPlaces = nil
}
func RecordUnlock() { recordMu.Unlock() }

// EmitCount 回傳本次記錄流程內 record_entry 被觸發(且成功寫入)的次數。
func EmitCount() int { return emitCount }

// EmittedIDs 回傳本次流程內已寫入的 entry ID(供建立與 message 的關聯)。
func EmittedIDs() []string { return emittedIDs }

// Presented 回傳本次流程內 present_entries 輸出的條目(供呼叫端回給前端)。
func Presented() []PresentedEntry { return presented }

// addPresented 由 present_entries 工具呼叫,累積要展示的條目。
func addPresented(es []PresentedEntry) { presented = append(presented, es...) }

// RecommendedPlaces 回傳本次流程內 recommend_nearby 查到的候選景點
// (供呼叫端隨 AssistResult 回給前端,掛在觸發它的那則訊息底下顯示)。
func RecommendedPlaces() []RecommendedPlace { return recommendedPlaces }

// addRecommendedPlaces 由 recommend_nearby 工具呼叫,累積本次查到的候選景點。
func addRecommendedPlaces(ps []RecommendedPlace) { recommendedPlaces = append(recommendedPlaces, ps...) }

// emit 由工具呼叫,同步把條目寫入 store(entry 為主體,獨立寫入)。
// channelID 由呼叫端透過 ChannelFrom(ctx) 取得後傳入,emit 本身不碰任何全域頻道狀態。
// 成功後記下 entry ID,供呼叫端在來源 message 寫入後建立關聯。
// 未注入 sink(例如測試)時不持久化,僅計數。
func emit(channelID string, e RecordedEntry) (string, error) {
	if sink == nil {
		emitCount++ // 測試情境:仍計數,讓「是否記錄」判斷可運作。
		return "", nil
	}
	id, err := sink(channelID, e)
	if err != nil {
		return "", err
	}
	emittedIDs = append(emittedIDs, id)
	emitCount++
	return id, nil
}
