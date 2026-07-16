package wanttools

import (
	"fmt"

	"github.com/tim72117/want/types"
)

func init() {
	types.RegisterTool(PresentEntriesDeclaration, func() types.ToolInterface {
		return &PresentEntriesTool{}
	})
}

var PresentEntriesDeclaration = types.ToolDeclaration{
	Name: "entry_present",
	Description: "把一筆要展示給使用者的條目加入展示清單。" +
		"回答查詢、列出安排/待辦/行程時,每一筆條目呼叫一次此工具(有幾筆就呼叫幾次)," +
		"前端會把這些條目用卡片列表顯示,比純文字更清楚。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "STRING",
				"description": "事項描述,例如 '開會討論 Q3 預算'。",
			},
			"start": map[string]interface{}{
				"type":        "STRING",
				"description": "開始日期 'YYYY-MM-DD'。直接用查到的條目值。",
			},
			"startTime": map[string]interface{}{
				"type":        "STRING",
				"description": "開始時刻 'HH:MM'。全日事件留空字串。",
			},
			"end": map[string]interface{}{
				"type":        "STRING",
				"description": "結束日期 'YYYY-MM-DD';無則留空字串。",
			},
			"endTime": map[string]interface{}{
				"type":        "STRING",
				"description": "結束時刻 'HH:MM';無則留空字串。",
			},
		},
		"required": []string{"title"},
	},
}

type PresentEntriesTool struct {
	types.BaseToolConfig
}

func (t *PresentEntriesTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	if args.GetString("title") == "" {
		return fmt.Errorf("title is required")
	}
	return nil
}

func (t *PresentEntriesTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	e := PresentedEntry{
		Title:     args.GetString("title"),
		Start:     args.GetString("start"),
		StartTime: args.GetString("startTime"),
		End:       args.GetString("end"),
		EndTime:   args.GetString("endTime"),
	}
	addPresented([]PresentedEntry{e})

	summary := fmt.Sprintf("Added to display: %s", e.Title)
	ctx.EmitToolResult(map[string]interface{}{"summary": summary})
	return []types.ResultContentBlock{types.TextBlock(summary)}, nil
}

func (t *PresentEntriesTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Displaying entry: %s", args.GetString("title"))
}

func (t *PresentEntriesTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to display entry: %v", err)
}

func (t *PresentEntriesTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["summary"].(string); ok {
		return msg
	}
	return "Entry added to display"
}
