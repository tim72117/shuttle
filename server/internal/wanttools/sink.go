package wanttools

import "sync"

// RecordedEntry 是 record_entry 工具解析出的一筆條目。
type RecordedEntry struct {
	Item      string
	Start     string // 'YYYY-MM-DD'
	StartTime string // 'HH:MM';空=全日
	End       string // 'YYYY-MM-DD'
	EndTime   string // 'HH:MM'
}

// EntrySink 同步把一筆條目寫入 store(entry 為主體,獨立寫入),
// 回傳新建 entry 的 ID 供之後與來源 message 建立關聯。
// server 啟動時用 BindSink 注入。
type EntrySink func(channelID string, e RecordedEntry) (entryID string, err error)

// PresentedEntry 是 present_entries 工具輸出的一筆「要展示給使用者」的條目。
type PresentedEntry struct {
	Item      string `json:"item"`
	Start     string `json:"start"`
	StartTime string `json:"startTime"`
	End       string `json:"end"`
	EndTime   string `json:"endTime"`
}

// NotifyFn 廣播 entries_updated 事件給前端(server 啟動時用 BindNotify 注入)。
type NotifyFn func(channelID string)

var (
	// recordMu 序列化整個「記錄一則訊息」的流程,確保 context 與工具呼叫不交錯。
	recordMu sync.Mutex
	sink     EntrySink
	notifyFn NotifyFn
	curMsgID string
	curChnID string
	// emitCount 記本次 RecordLock 流程內 record_entry 被觸發的次數,
	// 供呼叫端判斷 agent 究竟「記錄了」還是「只回答」。
	emitCount int
	// emittedIDs 收集本次流程內 emit 成功寫入的 entry ID,
	// 供呼叫端在來源 message 寫入後,逐一建立 entry↔message 關聯。
	emittedIDs []string
	// presented 收集本次流程內 present_entries 工具輸出的條目,
	// 由呼叫端取出回傳前端用列表元件顯示。
	presented []PresentedEntry
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

// CurrentChannel 回傳目前記錄 context 的頻道 ID。
// query_entries 工具用它得知「現在查哪個頻道」(agent 不需自己帶 channelID)。
func CurrentChannel() string { return curChnID }

// RecordLock / RecordUnlock 包住一次完整的記錄流程(設定 context → 跑 agent → 清除)。
// RecordLock 同時把本次的 emit 計數、已寫入 ID、展示條目歸零。
func RecordLock()   { recordMu.Lock(); emitCount = 0; emittedIDs = nil; presented = nil }
func RecordUnlock() { recordMu.Unlock() }

// EmitCount 回傳本次記錄流程內 record_entry 被觸發(且成功寫入)的次數。
func EmitCount() int { return emitCount }

// EmittedIDs 回傳本次流程內已寫入的 entry ID(供建立與 message 的關聯)。
func EmittedIDs() []string { return emittedIDs }

// Presented 回傳本次流程內 present_entries 輸出的條目(供呼叫端回給前端)。
func Presented() []PresentedEntry { return presented }

// addPresented 由 present_entries 工具呼叫,累積要展示的條目。
func addPresented(es []PresentedEntry) { presented = append(presented, es...) }

// SetContext 設定本次記錄對應的訊息(在 RecordLock 之後、Submit 之前呼叫)。
func SetContext(messageID, channelID string) {
	curMsgID, curChnID = messageID, channelID
}

// ClearContext 清除 context(本次記錄結束後)。
func ClearContext() { curMsgID, curChnID = "", "" }

// emit 由工具呼叫,同步把條目寫入 store(entry 為主體,獨立寫入)。
// 成功後記下 entry ID,供呼叫端在來源 message 寫入後建立關聯。
// 未注入 sink(例如測試)時不持久化,僅計數。
// emit 同步寫入 entry,回傳新 entry 的 ID。
func emit(e RecordedEntry) (string, error) {
	if sink == nil {
		emitCount++ // 測試情境:仍計數,讓「是否記錄」判斷可運作。
		return "", nil
	}
	id, err := sink(curChnID, e)
	if err != nil {
		return "", err
	}
	emittedIDs = append(emittedIDs, id)
	emitCount++
	return id, nil
}
