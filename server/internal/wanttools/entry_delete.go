package wanttools

import (
	"errors"
	"fmt"

	"github.com/channel/server/internal/store"
	"github.com/tim72117/want/types"
)

var DeleteEntryDeclaration = types.ToolDeclaration{
	Name: "entry_delete",
	Description: "刪除已記錄的條目。刪除前請先用 entry_query 確認 ID,並向使用者確認後再執行。" +
		"要刪除多筆(如「刪除全部」)時,用 entryIDs 一次帶入所有 ID,**不要**分成多次呼叫。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"entryIDs": map[string]interface{}{
				"type":        "ARRAY",
				"items":       map[string]interface{}{"type": "STRING"},
				"description": "要刪除的條目 ID 陣列(如 ['ent_a','ent_b'])。刪除多筆時用此欄位一次帶入全部,一次呼叫刪完。",
			},
			"entryID": map[string]interface{}{
				"type":        "STRING",
				"description": "要刪除的單一條目 ID(如 'ent_xxxx')。只刪一筆時用此欄位;刪多筆請改用 entryIDs。",
			},
		},
	},
}

type DeleteEntryTool struct {
	types.BaseToolConfig
}

// collectIDs 蒐集本次要刪除的 ID:優先取 entryIDs(陣列),否則退回 entryID(單筆);
// 全部正規化(補 'ent_' 前綴)並去除空值。
func (t *DeleteEntryTool) collectIDs(args types.ToolArguments) []string {
	var raw []string
	if arr := args.GetStringArray("entryIDs"); len(arr) > 0 {
		raw = arr
	} else if single := args.GetString("entryID"); single != "" {
		raw = []string{single}
	}
	var ids []string
	for _, id := range raw {
		norm := normalizeEntryID(id)
		if norm != "ent_" {
			ids = append(ids, norm)
		}
	}
	return ids
}

func (t *DeleteEntryTool) ValidateInput(args types.ToolArguments, ctx types.ToolContext) error {
	if entryStore == nil {
		return fmt.Errorf("store not initialized")
	}
	ids := t.collectIDs(args)
	if len(ids) == 0 {
		return fmt.Errorf("entryID or entryIDs is required")
	}
	wantChan := ChannelFrom(ctx)
	// 逐一確認存在且屬於目前頻道;任一不符就擋下整批(避免刪到一半才發現 ID 錯或跨頻道,
	// 回報也較清楚;不透露「該 ID 存在但屬於別的頻道」,一律回「找不到」避免資訊洩漏)。
	for _, id := range ids {
		entry, err := entryStore.GetEntry(id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("entry %s not found; use query_entries to get the correct entryID", id)
			}
			return fmt.Errorf("failed to look up entry %s: %w", id, err)
		}
		if entry.ChannelID != wantChan {
			return fmt.Errorf("entry %s not found; use query_entries to get the correct entryID", id)
		}
	}
	return nil
}

func (t *DeleteEntryTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if entryStore == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	ids := t.collectIDs(args)
	if len(ids) == 0 {
		return nil, fmt.Errorf("entryID or entryIDs is required")
	}

	var deleted []string
	for _, id := range ids {
		if err := entryStore.DeleteEntry(id); err != nil {
			// 中途失敗:回報已刪的與失敗的那筆,讓 agent 知道實際狀態(不謊稱全刪)。
			return nil, fmt.Errorf("deleted %d/%d; failed at %s: %w", len(deleted), len(ids), id, err)
		}
		deleted = append(deleted, id)
	}
	Notify(ChannelFrom(ctx))

	msg := fmt.Sprintf("Deleted %d entr%s: %v", len(deleted), plural(len(deleted)), deleted)
	ctx.EmitToolResult(map[string]interface{}{
		"message":      msg,
		"deletedIDs":   deleted,
		"deletedCount": len(deleted),
	})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func (t *DeleteEntryTool) RenderToolUse(args types.ToolArguments) string {
	ids := t.collectIDs(args)
	if len(ids) == 1 {
		return fmt.Sprintf("Deleting entry %s...", ids[0])
	}
	return fmt.Sprintf("Deleting %d entries...", len(ids))
}

func (t *DeleteEntryTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to delete entry: %v", err)
}

func (t *DeleteEntryTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Entry deleted"
}

func init() {
	types.RegisterTool(DeleteEntryDeclaration, func() types.ToolInterface {
		return &DeleteEntryTool{}
	})
}
