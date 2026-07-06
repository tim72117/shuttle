package wanttools

import (
	"fmt"
	"strings"

	"github.com/tim72117/want/types"
)

// TaskPlanDeclaration 是 task_plan 工具:讓 agent 規劃並追蹤多步驟任務。
// 任務存於記憶體(per-channel),供 agent 在處理複雜請求時:先列計畫、逐步完成、
// 全部完成後清除。用 action 分派各種操作,避免工具清單膨脹。
var TaskPlanDeclaration = types.ToolDeclaration{
	Name: "task_plan",
	Description: "規劃並追蹤多步驟任務(待辦清單,存於本頻道記憶體)。處理需要多步驟的複雜請求時," +
		"先用 create 列出計畫,完成一步就 complete 標記,全部完成後用 clear 清除。以 action 指定操作。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "STRING",
				"description": "操作類型:" +
					"'create'(新增任務;用 texts 一次列出整個計畫,或 text 加單筆)| " +
					"'list'(列出全部任務)| " +
					"'update'(改任務描述,需 id + text)| " +
					"'complete'(標記完成,需 id)| " +
					"'delete'(刪除一筆,需 id)| " +
					"'clear'(清空全部任務)。",
			},
			"texts": map[string]interface{}{
				"type":        "ARRAY",
				"items":       map[string]interface{}{"type": "STRING"},
				"description": "多筆任務描述(action=create 時,一次寫入整個計畫,如 ['步驟1','步驟2'])。規劃多步驟時用此欄位一次建立,不要多次呼叫。",
			},
			"text": map[string]interface{}{
				"type":        "STRING",
				"description": "單筆任務描述(action=create 加單筆、或 action=update 時需要)。",
			},
			"id": map[string]interface{}{
				"type":        "INTEGER",
				"description": "任務序號(action=update/complete/delete 時需要;由 create/list 回傳)。",
			},
		},
		"required": []string{"action"},
	},
}

type TaskPlanTool struct {
	types.BaseToolConfig
}

func (t *TaskPlanTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	action := args.GetString("action")
	switch action {
	case "create":
		if len(nonEmptyTexts(args)) == 0 {
			return fmt.Errorf("action=create 需要 text 或 texts")
		}
	case "update":
		if args.GetInt("id") == 0 {
			return fmt.Errorf("action=update 需要 id")
		}
		if strings.TrimSpace(args.GetString("text")) == "" {
			return fmt.Errorf("action=update 需要 text")
		}
	case "complete", "delete":
		if args.GetInt("id") == 0 {
			return fmt.Errorf("action=%s 需要 id", action)
		}
	case "list", "clear":
		// 無額外必填
	default:
		return fmt.Errorf("不支援的 action %q(可用:create/list/update/complete/delete/clear)", action)
	}
	return nil
}

func (t *TaskPlanTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	ch := ChannelFrom(ctx)
	action := args.GetString("action")

	var msg string
	switch action {
	case "create":
		texts := nonEmptyTexts(args)
		created := tasks.CreateMany(ch, texts)
		if len(created) == 1 {
			msg = fmt.Sprintf("已新增任務 #%d:%s", created[0].ID, created[0].Text)
		} else {
			msg = fmt.Sprintf("已新增 %d 筆任務", len(created))
		}
	case "update":
		id := args.GetInt("id")
		if err := tasks.Update(ch, id, args.GetString("text")); err != nil {
			return nil, err
		}
		msg = fmt.Sprintf("已更新任務 #%d", id)
	case "complete":
		id := args.GetInt("id")
		if err := tasks.Complete(ch, id); err != nil {
			return nil, err
		}
		msg = fmt.Sprintf("已標記任務 #%d 完成", id)
		if tasks.AllDone(ch) {
			msg += "(所有任務皆已完成,可呼叫 action=clear 清除)"
		}
	case "delete":
		id := args.GetInt("id")
		if err := tasks.Delete(ch, id); err != nil {
			return nil, err
		}
		msg = fmt.Sprintf("已刪除任務 #%d", id)
	case "clear":
		tasks.Clear(ch)
		msg = "已清除所有任務"
	case "list":
		msg = renderTaskList(tasks.List(ch))
	}

	// list 之外的操作,結尾附上最新清單,讓 agent 隨時看到當前狀態。
	if action != "list" && action != "clear" {
		msg += "\n" + renderTaskList(tasks.List(ch))
	}

	ctx.EmitToolResult(map[string]interface{}{"message": msg})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

// nonEmptyTexts 蒐集 create 要新增的任務描述:優先取 texts[](批次),
// 否則退回 text(單筆);去除空白項,保持原順序。
func nonEmptyTexts(args types.ToolArguments) []string {
	var raw []string
	if arr := args.GetStringArray("texts"); len(arr) > 0 {
		raw = arr
	} else if single := args.GetString("text"); single != "" {
		raw = []string{single}
	}
	var out []string
	for _, s := range raw {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// renderTaskList 把任務清單排成給 agent 閱讀的文字(含完成狀態勾選)。
func renderTaskList(list []Task) string {
	if len(list) == 0 {
		return "(目前沒有任務)"
	}
	var sb strings.Builder
	sb.WriteString("目前任務:")
	for _, t := range list {
		box := "[ ]"
		if t.Done {
			box = "[x]"
		}
		sb.WriteString(fmt.Sprintf("\n%s #%d %s", box, t.ID, t.Text))
	}
	return sb.String()
}

func (t *TaskPlanTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("task_plan: %s", args.GetString("action"))
}

func (t *TaskPlanTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("task_plan failed: %v", err)
}

func (t *TaskPlanTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "task_plan done"
}

func init() {
	types.RegisterTool(TaskPlanDeclaration, func() types.ToolInterface {
		return &TaskPlanTool{}
	})
}
