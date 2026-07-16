// Package wanttools 提供註冊到 want 引擎的自訂 agent 工具。
// 被 blank import 時,init() 會把工具註冊進 want 的全域 toolbox,
// 之後 LLM agent 在推論時即可呼叫(general role 的 Tools: ["*"] 允許所有工具)。
package wanttools

import (
	"fmt"
	"time"

	"github.com/tim72117/want/types"
)

func init() {
	types.RegisterTool(RecordEntryDeclaration, func() types.ToolInterface {
		return &RecordEntryTool{}
	})
}

var RecordEntryDeclaration = types.ToolDeclaration{
	Name: "entry_add",
	Description: "將一則項目記錄成帶有日期時間的條目並保存。" +
		"當使用者想把訊息存成待辦、行程、備忘或日誌條目時使用。每呼叫一次新增一筆。" +
		"請從訊息解析出事件的時間,可以是單一時間點、時間範圍或全日事件。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "STRING",
				"description": "要記錄的事項內容(去掉時間後的描述),例如:'開會討論 Q3 預算'",
			},
			"start": map[string]interface{}{
				"type": "STRING",
				"description": "事件【日期】,用英文自然語言語詞表達(只給日期,不含時刻),系統自動換算。" +
					"例如:'next Monday'、'tomorrow'、'this Friday'、'in 3 days'、'June 30'。" +
					"不要自己算日期。沒提到日期就留空字串。",
			},
			"startTime": map[string]interface{}{
				"type": "STRING",
				"description": "事件開始的時刻,24 小時制 'HH:MM'(如 '10:00'、'15:30')。" +
					"從使用者訊息直接取時刻,不要換算。沒提到時刻(全日事件)就留空字串。",
			},
			"end": map[string]interface{}{
				"type": "STRING",
				"description": "事件結束【日期】,格式同 start(英文日期語詞)。" +
					"只有表達日期範圍(如「6/30 到 7/2」)時才填,否則留空字串。",
			},
			"endTime": map[string]interface{}{
				"type": "STRING",
				"description": "事件結束的時刻,24 小時制 'HH:MM'。" +
					"只有表達時刻範圍(如「三點到五點」)時才填,否則留空字串。",
			},
			"kind": map[string]interface{}{
				"type": "STRING",
				"description": "條目類型(可留空)。目前支援:'stay'(住宿)。" +
					"選 'stay' 時必須同時提供 start(入住日)與 end(退房日);" +
					"未給時刻時系統自動補 check-in 15:00 / check-out 11:00。",
			},
			"taskID": map[string]interface{}{
				"type": "INTEGER",
				"description": "若這筆記錄是在完成 task_plan 規劃的某個步驟時新增的," +
					"帶上該步驟的任務序號(id),讓前端把對應的「新增中」佔位卡換成正式條目卡。" +
					"與 task_plan 無關的一般記錄留空。",
			},
		},
		"required": []string{"title"},
	},
}

type RecordEntryTool struct {
	types.BaseToolConfig
}

func (t *RecordEntryTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	if args.GetString("title") == "" {
		return fmt.Errorf("title is required")
	}
	// 委派給對應 kind 的策略做專屬欄位檢查(如 stay 需 start+end);
	// 無 kind 或未知 kind 時視為通過。
	return validateKind(args)
}

func (t *RecordEntryTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	// 先套用 kind 專屬預設值(如 stay 補 check-in/out 時刻),再讀取欄位。
	applyKindDefaults(args)

	title := args.GetString("title")
	kind := args.GetString("kind")
	now := time.Now()
	startDate := resolveDate(args.GetString("start"), now)
	startTime := args.GetString("startTime")
	endDate := resolveDate(args.GetString("end"), now)
	endTime := args.GetString("endTime")

	channelID := ChannelFrom(ctx)
	entryID, err := emit(channelID, RecordedEntry{
		Title: title, Start: startDate, StartTime: startTime, End: endDate, EndTime: endTime, Kind: kind,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to save entry: %w", err)
	}

	if taskID := args.GetInt("taskID"); taskID != 0 {
		NotifyTaskEntryReady(channelID, taskID, entryID)
	}

	resultMsg := fmt.Sprintf("Recorded (entryID=%s): %s %s", entryID, describeTime(startDate, startTime, endDate, endTime), title)

	ctx.EmitToolResult(map[string]interface{}{
		"message":   resultMsg,
		"entryID":   entryID,
		"start":     startDate,
		"startTime": startTime,
		"end":       endDate,
		"endTime":   endTime,
		"title":     title,
	})
	return []types.ResultContentBlock{types.TextBlock(resultMsg)}, nil
}

func (t *RecordEntryTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Recording entry: %s", args.GetString("title"))
}

func (t *RecordEntryTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to record entry: %v", err)
}

func (t *RecordEntryTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Entry recorded"
}

func describeTime(start, startTime, end, endTime string) string {
	if start == "" {
		return "(no time)"
	}
	allDay := startTime == ""
	s := start
	if !allDay {
		s += " " + startTime
	}
	if end == "" {
		if allDay {
			return s + " (all-day)"
		}
		return s
	}
	e := end
	if endTime != "" {
		e += " " + endTime
	}
	if allDay {
		return s + " ~ " + e + " (all-day)"
	}
	return s + " ~ " + e
}
