package wanttools

import (
	"fmt"
	"strings"

	"github.com/tim72117/want/types"
)

// AskChoiceDeclaration 是 ask_choice 工具:當 agent 缺乏明確資訊、需要使用者從
// 幾個選項中選一個時(如多個房型、多個候選行程擇一),呼叫此工具請使用者透過前端
// UI 選單挑選,而不是用文字列點的方式詢問或憑猜測選一個。
//
// 非同步設計(比照 ask_user):本工具不等待使用者回答——呼叫後立即透過 WS 推送
// ask_choice 事件給前端開啟選單 UI,並回傳「已請使用者選擇」讓 agent 結束本輪。
// 使用者在 UI 選定某個選項後,前端把該選項的 title 當成一則新訊息送回聊天記錄,
// agent 重新推論(此時資訊已齊全,靠對話歷史接上前文)。
var AskChoiceDeclaration = types.ToolDeclaration{
	Name: "ask_choice",
	Description: "當缺乏明確資訊、需要使用者從幾個選項中選一個時(如多個房型、多個候選行程擇一)," +
		"呼叫此工具請使用者透過 UI 選單挑選一個。不要用文字列點的方式詢問,也不要憑猜測選一個。" +
		"呼叫後本輪對話結束,使用者選定後會再次觸發你,屆時再依選定結果繼續完成。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"prompt": map[string]interface{}{
				"type":        "STRING",
				"description": "顯示給使用者的問題文字,說明要選什麼,例如「請選擇房型」。",
			},
			"options": map[string]interface{}{
				"type": "ARRAY",
				"items": map[string]interface{}{
					"type": "OBJECT",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type":        "STRING",
							"description": "選項的主標題,例如「雙人房」。",
						},
						"description": map[string]interface{}{
							"type":        "STRING",
							"description": "選項的一行描述(可選),例如「約 25 坪,含早餐」。",
						},
					},
					"required": []string{"title"},
				},
				"description": "供使用者挑選的選項清單,每個元素是 {title, description} 物件。" +
					"陣列長度不限,但至少需要 2 個選項(只有 1 個選項就沒有選擇的必要)。",
			},
		},
		"required": []string{"prompt", "options"},
	},
}

type AskChoiceTool struct {
	types.BaseToolConfig
}

func (t *AskChoiceTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	if strings.TrimSpace(args.GetString("prompt")) == "" {
		return fmt.Errorf("prompt is required")
	}
	options := collectAskChoiceOptions(args)
	if len(options) < 2 {
		return fmt.Errorf("options requires at least 2 items")
	}
	for _, o := range options {
		if strings.TrimSpace(o.Title) == "" {
			return fmt.Errorf("every option requires a non-empty title")
		}
	}
	return nil
}

func (t *AskChoiceTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	prompt := args.GetString("prompt")
	options := collectAskChoiceOptions(args)

	// 透過 WS 推送給前端開啟選單 UI(非同步:不等回答)。
	NotifyAskChoice(ChannelFrom(ctx), prompt, options)

	msg := fmt.Sprintf("已請使用者從 %d 個選項中選擇「%s」。本輪結束,待使用者選定後會再次觸發。", len(options), prompt)
	ctx.EmitToolResult(map[string]interface{}{
		"message": msg,
		"prompt":  prompt,
		"options": options,
	})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

// collectAskChoiceOptions 從 args["options"](陣列,每筆是 {title, description} 物件)
// 解析成 AskChoiceOption 清單;非物件或缺 title 的項目會被跳過(交由 ValidateInput
// 統一擋下不合法輸入,這裡只單純轉型)。
func collectAskChoiceOptions(args types.ToolArguments) []AskChoiceOption {
	raw, _ := args["options"].([]interface{})
	if len(raw) == 0 {
		return nil
	}
	out := make([]AskChoiceOption, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, AskChoiceOption{
			Title:       types.ToolArguments(obj).GetString("title"),
			Description: types.ToolArguments(obj).GetString("description"),
		})
	}
	return out
}

func (t *AskChoiceTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Asking user to choose: %s", args.GetString("prompt"))
}

func (t *AskChoiceTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to ask user to choose: %v", err)
}

func (t *AskChoiceTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Asked user to choose"
}

func init() {
	types.RegisterTool(AskChoiceDeclaration, func() types.ToolInterface {
		return &AskChoiceTool{}
	})
}
