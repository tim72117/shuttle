package trip

import (
	"fmt"
	"strings"

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

func (t *DeleteTripTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	tripID := args.GetString("tripID")
	if tripID == "" {
		return fmt.Errorf("tripID is required")
	}
	if !strings.HasPrefix(tripID, "trip_") {
		return fmt.Errorf("invalid tripID %q: must start with 'trip_'", tripID)
	}
	return nil
}

func (t *DeleteTripTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if tripService == nil {
		return nil, fmt.Errorf("trip service not initialized")
	}
	tripID := args.GetString("tripID")
	if err := tripService.DeleteTrip(tripID); err != nil {
		return nil, fmt.Errorf("failed to delete trip: %w", err)
	}
	msg := fmt.Sprintf("Trip %s deleted", tripID)
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "tripID": tripID})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *DeleteTripTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Deleting trip %s...", args.GetString("tripID"))
}

func (t *DeleteTripTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to delete trip: %v", err)
}

func (t *DeleteTripTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Trip deleted"
}

func init() {
	types.RegisterTool(DeleteTripDeclaration, func() types.ToolInterface {
		return &DeleteTripTool{}
	})
}
