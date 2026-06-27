package wanttools

import (
	"fmt"

	"github.com/tim72117/want/types"
)

var DeleteTripDeclaration = types.ToolDeclaration{
	Name:        "delete_trip",
	Description: "刪除一個行程（Trip）及其所有關聯。請先透過 list_trips 取得 tripID 再呼叫。刪除前應向使用者確認。",
	Type:        "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"tripID": map[string]interface{}{
				"type":        "STRING",
				"description": "要刪除的行程 ID（如 'trip_xxxx'）。",
			},
		},
		"required": []string{"tripID"},
	},
}

type DeleteTripTool struct {
	types.BaseToolConfig
}

func (t *DeleteTripTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if tripService == nil {
		return nil, fmt.Errorf("行程服務未初始化")
	}
	tripID := args.GetString("tripID")
	if tripID == "" {
		return nil, fmt.Errorf("tripID 不可為空")
	}
	if err := tripService.DeleteTrip(tripID); err != nil {
		return nil, fmt.Errorf("刪除行程失敗: %w", err)
	}
	msg := fmt.Sprintf("已刪除行程 %s", tripID)
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "tripID": tripID})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *DeleteTripTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("正在刪除行程 %s...", args.GetString("tripID"))
}

func (t *DeleteTripTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("刪除行程失敗：%v", err)
}

func (t *DeleteTripTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "已刪除行程"
}

func init() {
	types.RegisterTool(DeleteTripDeclaration, func() types.ToolInterface {
		return &DeleteTripTool{}
	})
}
