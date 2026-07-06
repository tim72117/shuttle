package wanttools

import "testing"

// TestEmitWritesSynchronously 驗證新模型:emit 同步寫入 entry,
// 並把回傳的 entry ID 記進 EmittedIDs(供之後關聯 message)。
func TestEmitWritesSynchronously(t *testing.T) {
	RecordLock()
	defer RecordUnlock()

	var written []RecordedEntry
	var gotChannelID string
	BindSink(func(channelID string, e RecordedEntry) (string, error) {
		gotChannelID = channelID
		written = append(written, e)
		return "ent_" + e.Item, nil // 假 ID
	})
	t.Cleanup(func() { BindSink(nil) })

	if _, err := emit("ch_1", RecordedEntry{Item: "開會"}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if _, err := emit("ch_1", RecordedEntry{Item: "交報告"}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	// emit 應「同步」寫入,當下 sink 就被呼叫,且收到呼叫端傳入的 channelID。
	if len(written) != 2 {
		t.Fatalf("emit 應同步寫入 2 筆,實得 %d", len(written))
	}
	if gotChannelID != "ch_1" {
		t.Fatalf("sink 收到的 channelID = %q, want %q", gotChannelID, "ch_1")
	}
	if EmitCount() != 2 {
		t.Fatalf("EmitCount = %d, want 2", EmitCount())
	}
	// 已寫入的 entry ID 應被記錄(供關聯 message)。
	ids := EmittedIDs()
	if len(ids) != 2 || ids[0] != "ent_開會" || ids[1] != "ent_交報告" {
		t.Fatalf("EmittedIDs 不符: %+v", ids)
	}
}

// TestEmitNoSinkStillCounts 驗證未注入 sink(測試/rule 模式)時,
// emit 不報錯且仍計數(讓「是否記錄」判斷可運作)。
func TestEmitNoSinkStillCounts(t *testing.T) {
	RecordLock()
	defer RecordUnlock()
	BindSink(nil)

	if _, err := emit("ch_x", RecordedEntry{Item: "x"}); err != nil {
		t.Fatalf("無 sink 時 emit 不應報錯: %v", err)
	}
	if EmitCount() != 1 {
		t.Fatalf("無 sink 仍應計數, EmitCount=%d", EmitCount())
	}
	if len(EmittedIDs()) != 0 {
		t.Fatalf("無 sink 不應有 emitted ID")
	}
}

// TestRecordLockResets 驗證新一輪 RecordLock 會清掉上一輪的計數與 ID,
// 避免跨請求殘留。
func TestRecordLockResets(t *testing.T) {
	BindSink(func(channelID string, e RecordedEntry) (string, error) { return "ent_x", nil })
	t.Cleanup(func() { BindSink(nil) })

	RecordLock()
	_, _ = emit("ch_a", RecordedEntry{Item: "殘留"})
	RecordUnlock()

	RecordLock()
	defer RecordUnlock()
	if EmitCount() != 0 || len(EmittedIDs()) != 0 {
		t.Fatalf("RecordLock 應清空計數與 ID, EmitCount=%d ids=%d", EmitCount(), len(EmittedIDs()))
	}
}

// TestChannelFromNilOrEmptyCtx 驗證 ChannelFrom 在 ctx 為 nil 或無 SessionEnvs 時
// 回空字串,不會 panic(呼叫端如 CLI 等尚未走完整 orchestrator 流程的路徑會遇到)。
func TestChannelFromNilOrEmptyCtx(t *testing.T) {
	if got := ChannelFrom(nil); got != "" {
		t.Fatalf("ChannelFrom(nil) = %q, want empty", got)
	}
}
