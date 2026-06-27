package wanttools

import (
	"fmt"
	"strings"

	"github.com/tim72117/want/types"
)

var ListTripsDeclaration = types.ToolDeclaration{
	Name:        "list_trips",
	Description: "列出頻道中所有行程（Trip）的清單，包含每個行程的 ID、標題與時間範圍。在管理或查詢行程前先呼叫以取得 tripID。",
	Type:        "sync",
	Parameters: map[string]interface{}{
		"type":       "OBJECT",
		"properties": map[string]interface{}{},
		"required":   []string{},
	},
}

type ListTripsTool struct {
	types.BaseToolConfig
}

func (t *ListTripsTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	if tripService == nil {
		return nil, fmt.Errorf("行程服務未初始化")
	}
	trips, err := tripService.ListTrips(CurrentChannel())
	if err != nil {
		return nil, fmt.Errorf("查詢行程失敗: %w", err)
	}
	if len(trips) == 0 {
		msg := "目前沒有任何行程"
		ctx.EmitToolResult(map[string]interface{}{"message": msg, "trips": []interface{}{}})
		return []types.ResultContentBlock{types.TextBlock(msg)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("共 %d 個行程：\n", len(trips)))
	tripList := make([]map[string]interface{}, 0, len(trips))
	for _, tr := range trips {
		rng := tr.Start
		if tr.End != "" {
			rng += " ~ " + tr.End
		}
		sb.WriteString(fmt.Sprintf("・tripID=%s 「%s」(%s)\n", tr.ID, tr.Title, rng))
		tripList = append(tripList, map[string]interface{}{
			"tripID": tr.ID,
			"title":  tr.Title,
			"start":  tr.Start,
			"end":    tr.End,
		})
	}
	msg := strings.TrimRight(sb.String(), "\n")
	ctx.EmitToolResult(map[string]interface{}{"message": msg, "trips": tripList})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *ListTripsTool) RenderToolUse(_ types.ToolArguments) string {
	return "正在列出行程..."
}

func (t *ListTripsTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("列出行程失敗：%v", err)
}

func (t *ListTripsTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "已取得行程清單"
}

func init() {
	types.RegisterTool(ListTripsDeclaration, func() types.ToolInterface {
		return &ListTripsTool{}
	})
}
