package wanttools

import (
	"fmt"

	"github.com/channel/server/internal/tripsvc"
	"github.com/tim72117/want/types"
)

var UpdateEntryDeclaration = types.ToolDeclaration{
	Name: "update_entry",
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
				"description": "新的開始時間，格式 'YYYY-MM-DD' 或 'YYYY-MM-DD HH:MM'，留空字串表示不修改。",
			},
			"end": map[string]interface{}{
				"type":        "STRING",
				"description": "新的結束時間，格式同 start，留空字串表示不修改。",
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

func (t *UpdateEntryTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if tripService == nil {
		return nil, fmt.Errorf("行程服務未初始化")
	}
	entryID := args.GetString("entryID")
	if entryID == "" {
		return nil, fmt.Errorf("entryID 不可為空")
	}
	err := tripService.UpdateEntry(tripsvc.UpdateEntryInput{
		ID:       entryID,
		Item:     args.GetString("item"),
		Start:    args.GetString("start"),
		End:      args.GetString("end"),
		Location: args.GetString("location"),
		Kind:     args.GetString("kind"),
	})
	if err != nil {
		return nil, fmt.Errorf("更新條目失敗: %w", err)
	}
	msg := fmt.Sprintf("已更新條目 %s", entryID)
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "entryID": entryID})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *UpdateEntryTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("正在更新條目 %s...", args.GetString("entryID"))
}

func (t *UpdateEntryTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("更新條目失敗：%v", err)
}

func (t *UpdateEntryTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "已更新條目"
}

func init() {
	types.RegisterTool(UpdateEntryDeclaration, func() types.ToolInterface {
		return &UpdateEntryTool{}
	})
}
