package wanttools

import (
	"fmt"

	"github.com/tim72117/want/types"
)

var DeleteEntryDeclaration = types.ToolDeclaration{
	Name:        "entry_delete",
	Description: "刪除一筆已記錄的條目。刪除前請先用 entry_query 確認 entryID，並向使用者確認後再執行。",
	Type:        "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"entryID": map[string]interface{}{
				"type":        "STRING",
				"description": "要刪除的條目 ID（如 'ent_xxxx'）。",
			},
		},
		"required": []string{"entryID"},
	},
}

type DeleteEntryTool struct {
	types.BaseToolConfig
}

func (t *DeleteEntryTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	entryID := normalizeEntryID(args.GetString("entryID"))
	if entryID == "ent_" {
		return fmt.Errorf("entryID is required")
	}
	if entryStore == nil {
		return fmt.Errorf("store not initialized")
	}
	exists, err := entryStore.EntryExists(entryID)
	if err != nil {
		return fmt.Errorf("failed to look up entry: %w", err)
	}
	if !exists {
		return fmt.Errorf("entry %s not found; use query_entries to get the correct entryID", entryID)
	}
	return nil
}

func (t *DeleteEntryTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if entryStore == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	entryID := normalizeEntryID(args.GetString("entryID"))
	if err := entryStore.DeleteEntry(entryID); err != nil {
		return nil, fmt.Errorf("failed to delete entry: %w", err)
	}
	Notify(CurrentChannel())
	msg := fmt.Sprintf("Entry %s deleted", entryID)
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "entryID": entryID})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *DeleteEntryTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Deleting entry %s...", args.GetString("entryID"))
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
