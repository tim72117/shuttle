package wanttools

import (
	"fmt"

	"github.com/tim72117/want/types"
)

// AskUserDeclaration 是 ask_user 工具:當 agent 缺少必要資訊(如住宿的退房日)時,
// 呼叫此工具請使用者透過前端 UI 補上,而不是憑猜測填值或硬送一個不完整的呼叫。
//
// 非同步設計:本工具不等待使用者回答——呼叫後立即透過 WS 推送 ask_user 事件給前端
// 開啟對應 UI,並回傳「已請使用者提供」讓 agent 結束本輪。使用者在 UI 選好後,
// 前端把選到的值當成一則新訊息送回,agent 重新推論(此時資訊已齊全,靠對話歷史接上前文)。
var AskUserDeclaration = types.ToolDeclaration{
	Name: "ask_user",
	Description: "當缺少記錄所需的必要資訊(如住宿的退房日期)時,呼叫此工具請使用者透過 UI 補上。" +
		"不要憑猜測填缺失的值。呼叫後本輪對話結束,使用者補上資訊後會再次觸發你,屆時再完成記錄。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"askType": map[string]interface{}{
				"type": "STRING",
				"description": "要請使用者提供的資訊類型。目前支援:'date'(讓使用者選一個日期,如退房日)。",
			},
			"prompt": map[string]interface{}{
				"type": "STRING",
				"description": "顯示給使用者的提示文字,說明要提供什麼,例如「請選擇希爾頓的退房日期」。",
			},
		},
		"required": []string{"askType", "prompt"},
	},
}

type AskUserTool struct {
	types.BaseToolConfig
}

func (t *AskUserTool) ValidateInput(args types.ToolArguments, _ types.ToolContext) error {
	if args.GetString("askType") == "" {
		return fmt.Errorf("askType is required")
	}
	if args.GetString("prompt") == "" {
		return fmt.Errorf("prompt is required")
	}
	// 目前只支援 date;未支援的類型直接擋下,避免前端收到不認得的 UI 類型。
	if at := args.GetString("askType"); at != "date" {
		return fmt.Errorf("unsupported askType %q; currently only 'date' is supported", at)
	}
	return nil
}

func (t *AskUserTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	askType := args.GetString("askType")
	prompt := args.GetString("prompt")

	// 透過 WS 推送給前端開啟對應 UI(非同步:不等回答)。
	NotifyAskUser(ChannelFrom(ctx), askType, prompt)

	msg := fmt.Sprintf("已請使用者透過 UI 提供「%s」。本輪結束,待使用者補上後會再次觸發。", prompt)
	ctx.EmitToolResult(map[string]interface{}{
		"message": msg,
		"askType": askType,
		"prompt":  prompt,
	})
	return []types.ResultContentBlock{types.TextBlock(msg)}, nil
}

func (t *AskUserTool) RenderToolUse(args types.ToolArguments) string {
	return fmt.Sprintf("Asking user for %s: %s", args.GetString("askType"), args.GetString("prompt"))
}

func (t *AskUserTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("Failed to ask user: %v", err)
}

func (t *AskUserTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["message"].(string); ok {
		return msg
	}
	return "Asked user"
}

func init() {
	types.RegisterTool(AskUserDeclaration, func() types.ToolInterface {
		return &AskUserTool{}
	})
}
