package wanttools

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/channel/server/internal/store"
	"github.com/tim72117/want/types"
)

var UpdateEntryDeclaration = types.ToolDeclaration{
	Name: "entry_update",
	Description: "更新一筆已記錄條目的欄位（事項名稱、時間、地點、種類等）。" +
		"只需傳入要修改的欄位，未傳入的欄位保持原值不變。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"entryID": map[string]interface{}{
				"type":        "STRING",
				"description": "要更新的條目 ID（如 'ent_xxxx'）。",
			},
			"item": map[string]interface{}{
				"type":        "STRING",
				"description": "新的事項描述，留空字串表示不修改。",
			},
			"start": map[string]interface{}{
				"type":        "STRING",
				"description": "新的開始日期，英文自然語言語詞（如 'June 30'、'tomorrow'）或絕對格式 'YYYY-MM-DD'。留空字串表示不修改。",
			},
			"startTime": map[string]interface{}{
				"type":        "STRING",
				"description": "新的開始時刻，24 小時制 'HH:MM'（如 '09:00'）。留空字串表示不修改時刻。",
			},
			"end": map[string]interface{}{
				"type":        "STRING",
				"description": "新的結束日期，格式同 start。留空字串表示不修改。",
			},
			"endTime": map[string]interface{}{
				"type":        "STRING",
				"description": "新的結束時刻，24 小時制 'HH:MM'。留空字串表示不修改。",
			},
			"location": map[string]interface{}{
				"type":        "STRING",
				"description": "新的地點，留空字串表示不修改。",
			},
			"kind": map[string]interface{}{
				"type":        "STRING",
				"description": "條目種類：flight（飛行）、stay（住宿）、car（租車）、activity（活動）、food（餐飲）、transport（交通）、other（其他）。留空字串表示不修改。",
			},
		},
		"required": []string{"entryID"},
	},
}

type UpdateEntryTool struct {
	types.BaseToolConfig
}

func normalizeEntryID(id string) string {
	if !strings.HasPrefix(id, "ent_") {
		return "ent_" + id
	}
	return id
}

func (t *UpdateEntryTool) ValidateInput(args types.ToolArguments, ctx types.ToolContext) error {
	entryID := normalizeEntryID(args.GetString("entryID"))
	if entryID == "ent_" {
		return fmt.Errorf("entryID is required")
	}
	if entryStore == nil {
		return fmt.Errorf("store not initialized")
	}
	entry, err := entryStore.GetEntry(entryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("entry %s not found; use query_entries to get the correct entryID", entryID)
		}
		return fmt.Errorf("failed to look up entry: %w", err)
	}
	// 條目必須屬於目前頻道:防止(誤帶或猜測的)entryID 跨頻道更新到別的頻道資料。
	if wantChan := ChannelFrom(ctx); entry.ChannelID != wantChan {
		return fmt.Errorf("entry %s not found; use query_entries to get the correct entryID", entryID)
	}
	return nil
}

func (t *UpdateEntryTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	entryID := normalizeEntryID(args.GetString("entryID"))
	// 更新前先通知前端:對應條目卡片切成「更新中」動畫。
	// 更新完成後的 Notify(entries_updated) 會讓前端重抓並解除更新中狀態。
	NotifyEntryUpdating(ChannelFrom(ctx), entryID)
	now := time.Now()
	startDate := resolveDate(args.GetString("start"), now)
	err := entryStore.UpdateEntry(
		entryID,
		args.GetString("item"),
		startDate,
		args.GetString("startTime"),
		resolveDate(args.GetString("end"), now),
		args.GetString("endTime"),
		args.GetString("location"),
		"", // summary
		args.GetString("kind"),
		nil, // detail
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update entry: %w", err)
	}
	Notify(ChannelFrom(ctx))
	msg := fmt.Sprintf("Entry %s updated", entryID)
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "entryID": entryID})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *UpdateEntryTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Updating entry %s...", args.GetString("entryID"))
}

func (t *UpdateEntryTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to update entry: %v", err)
}

func (t *UpdateEntryTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Entry updated"
}

func init() {
	types.RegisterTool(UpdateEntryDeclaration, func() types.ToolInterface {
		return &UpdateEntryTool{}
	})
}
