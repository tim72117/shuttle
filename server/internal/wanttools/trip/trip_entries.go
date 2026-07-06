package trip

import (
	"fmt"
	"strings"

	"github.com/tim72117/want/types"
)

var TripEntriesDeclaration = types.ToolDeclaration{
	Name:        "trip_entries",
	Description: "列出指定行程（Trip）下的所有條目，包含時間、地點與類型。需先透過 list_trips 取得 tripID。",
	Type:        "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"tripID": map[string]interface{}{
				"type":        "STRING",
				"description": "行程 ID（如 'trip_xxxx'）。",
			},
		},
		"required": []string{"tripID"},
	},
}

type TripEntriesTool struct {
	types.BaseToolConfig
}

func (t *TripEntriesTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	tripID := args.GetString("tripID")
	if tripID == "" {
		return fmt.Errorf("tripID is required")
	}
	if !strings.HasPrefix(tripID, "trip_") {
		return fmt.Errorf("invalid tripID %q: must start with 'trip_'", tripID)
	}
	return nil
}

func (t *TripEntriesTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if tripService == nil {
		return nil, fmt.Errorf("trip service not initialized")
	}
	tripID := args.GetString("tripID")
	entries, err := tripService.ListTripEntries(currentChannel(ctx), tripID)
	if err != nil {
		return nil, fmt.Errorf("failed to list trip entries: %w", err)
	}
	if len(entries) == 0 {
		msg := fmt.Sprintf("No entries in trip %s", tripID)
		ctx.EmitToolResult(map[string]interface{}{"message": msg, "entries": []interface{}{}})
		return []types.ResultContentBlock{types.TextBlock(msg)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Trip %s — %d entry(s):\n", tripID, len(entries)))
	entryList := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		line := fmt.Sprintf("・[entryID=%s] %s", e.ID, e.Item)
		if e.Start != "" {
			line += fmt.Sprintf(" (%s", e.Start)
			if e.End != "" && e.End != e.Start {
				line += " ~ " + e.End
			}
			line += ")"
		}
		if e.Location != "" {
			line += " @ " + e.Location
		}
		sb.WriteString(line + "\n")
		entryList = append(entryList, map[string]interface{}{
			"entryID":  e.ID,
			"item":     e.Item,
			"start":    e.Start,
			"end":      e.End,
			"location": e.Location,
			"kind":     e.Kind,
		})
	}
	msg := strings.TrimRight(sb.String(), "\n")
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "entries": entryList})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *TripEntriesTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Listing entries for trip %s...", args.GetString("tripID"))
}

func (t *TripEntriesTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to list trip entries: %v", err)
}

func (t *TripEntriesTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Trip entries listed"
}

func init() {
	types.RegisterTool(TripEntriesDeclaration, func() types.ToolInterface {
		return &TripEntriesTool{}
	})
}
