package trip

import (
	"fmt"
	"strings"

	"github.com/tim72117/want/types"
)

var AddToTripDeclaration = types.ToolDeclaration{
	Name: "add_to_trip",
	Description: "把一個已記錄的條目歸入某個行程(Trip),用於把同一趟旅程/連續安排的條目串在一起。" +
		"當 record_entry 列出了時間相符的候選行程、且你判斷該條目確實屬於其中之一時呼叫;" +
		"若都不相關但仍想成組,可不指定 tripID 以新建行程。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"entryID": map[string]interface{}{
				"type":        "STRING",
				"description": "要歸入行程的條目 ID(record_entry 結果中提供的 entryID,如 'ent_xxxx')。",
			},
			"tripID": map[string]interface{}{
				"type": "STRING",
				"description": "要歸入的既有行程 ID(record_entry 候選清單中提供的 tripID,如 'trip_xxxx')。" +
					"留空字串表示新建一個行程。",
			},
			"tripTitle": map[string]interface{}{
				"type":        "STRING",
				"description": "新建行程時的行程名(僅 tripID 留空時使用),如 '東京旅遊'。留空則用條目描述。",
			},
		},
		"required": []string{"entryID"},
	},
}

type AddToTripTool struct {
	types.BaseToolConfig
}

func (t *AddToTripTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	entryID := normalizeEntryID(args.GetString("entryID"))
	if entryID == "ent_" {
		return fmt.Errorf("entryID is required")
	}
	tripID := args.GetString("tripID")
	if tripID != "" && !strings.HasPrefix(tripID, "trip_") {
		return fmt.Errorf("invalid tripID %q: must start with 'trip_'", tripID)
	}
	return nil
}

func (t *AddToTripTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if tripService == nil {
		return nil, fmt.Errorf("trip service not initialized")
	}
	entryID := normalizeEntryID(args.GetString("entryID"))
	tripID, _, err := tripService.AddToTrip(entryID, args.GetString("tripID"), args.GetString("tripTitle"))
	if err != nil {
		return nil, fmt.Errorf("failed to add entry to trip: %w", err)
	}
	msg := fmt.Sprintf("Entry %s added to trip %s", entryID, tripID)
	ctx.EmitToolResult(map[string]interface{}{
		"message": msg,
		"entryID": entryID,
		"tripID":  tripID,
	})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *AddToTripTool) RenderToolUse(args types.ToolArguments) string {
	if args.GetString("tripID") == "" {
		return fmt.Sprintf("Creating trip for entry %s...", args.GetString("entryID"))
	}
	return fmt.Sprintf("Adding entry %s to trip %s...", args.GetString("entryID"), args.GetString("tripID"))
}

func (t *AddToTripTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to add entry to trip: %v", err)
}

func (t *AddToTripTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Entry added to trip"
}

func init() {
	types.RegisterTool(AddToTripDeclaration, func() types.ToolInterface {
		return &AddToTripTool{}
	})
}
