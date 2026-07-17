// Package llm 封裝「語意查詢回答」(由 want LLM 引擎實作)。
// 唯一的 Analyzer 實作是接 want 引擎的 WantPool;不再有規則式(非 LLM)分析器。
package llm

import (
	"github.com/tim72117/shuttle/internal/model"
)

// Analyzer 是 LLM 能力的抽象。唯一實作為接 want 引擎的 WantPool。
type Analyzer interface {
	// Answer 對某頻道做自然語言查詢:agent 依 assistant.md 指引,自己用
	// query_entries 查條目、再用 present_entries 呈現相關條目。
	// channelID 供 query_entries 工具定位要查的頻道。
	// lang 是使用者設定的回答語言偏好("zh-TW"/"en"),空字串視為預設(繁體中文)。
	Answer(channelID, question, lang string) model.SearchAnswer
}
