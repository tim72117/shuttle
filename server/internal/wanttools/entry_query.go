package wanttools

import (
	"fmt"
	"strings"
	"time"

	"github.com/channel/server/internal/store"
	"github.com/tim72117/want/types"
)

// entryStore 是工具共用的 store 實例(server 啟動時用 BindStore 注入)。
var entryStore *store.Store

// BindStore 注入 store 實例(server 啟動時呼叫)。
func BindStore(s *store.Store) {
	entryStore = s
}

func init() {
	types.RegisterTool(QueryEntriesDeclaration, func() types.ToolInterface {
		return &QueryEntriesTool{}
	})
}

// QueryEntriesDeclaration 是給 LLM 看的工具宣告。
// 用於查詢頻道中已記錄的條目(record_entry 記下的待辦/行程/會議),可選時間範圍。
var QueryEntriesDeclaration = types.ToolDeclaration{
	Name: "entry_query",
	Description: "查詢頻道中已記錄的條目(待辦、行程、會議等)。" +
		"當使用者在提問、想知道某段時間有什麼安排時呼叫。可用 from / to 限定時間範圍。" +
		"回傳符合的條目清單,據此回答使用者。",
	Type: "sync",
	Parameters: map[string]interface{}{
		"type": "OBJECT",
		"properties": map[string]interface{}{
			"from": map[string]interface{}{
				"type": "STRING",
				"description": "範圍起點,用【英文】自然語言時間語詞表達(系統自動換算)。" +
					"把時間範圍拆成起點與終點兩個時間點,例如「上週」→ from='last Monday'、to='last Sunday';" +
					"「下個月」→ from='next month' 的起訖兩日。可用 'last Monday'、'tomorrow'、'June 1' 等。" +
					"不限定起點就留空字串。",
			},
			"to": map[string]interface{}{
				"type": "STRING",
				"description": "範圍終點(含),格式同 from(英文時間語詞)。" +
					"查單一天時 from 與 to 填同一個英文語詞即可。不限定終點就留空字串。",
			},
		},
	},
}

type QueryEntriesTool struct {
	types.BaseToolConfig
}

// Call 執行查詢:回傳 []ResultContentBlock(want 新規格),結果 map 以 ctx.EmitToolResult 發送。
func (t *QueryEntriesTool) Call(args types.ToolArguments, ctx types.ToolContext) ([]types.ResultContentBlock, error) {
	// from / to 是 LLM 給的英文日期語詞(範圍以兩個日期點表達),用 when 換算成絕對日期。
	// 查詢只需日期粒度,不需時刻。
	now := time.Now()
	rawFrom, rawTo := args.GetString("from"), args.GetString("to")
	from := resolveDate(rawFrom, now)
	to := resolveDate(rawTo, now)

	// 範圍頭尾顛倒(from 晚於 to)→ 回 error 讓 agent 自己修正,而非默默對換。
	// 訊息給出解析後的實際日期,讓 agent 知道哪個語詞算錯、該怎麼改。
	if from != "" && to != "" && from > to {
		return nil, fmt.Errorf(
			"時間範圍顛倒:from '%s' 解析為 %s,晚於 to '%s' 解析為 %s。"+
				"請改用不會顛倒的語詞重新查詢(例如查「這週」用 from='last Monday'、to='next Sunday'),"+
				"確保 from 的日期早於或等於 to。今天是 %s。",
			rawFrom, from, rawTo, to, now.Format("2006-01-02"))
	}

	if entryStore == nil {
		return nil, fmt.Errorf("store 未初始化")
	}
	// channelID 取自當前記錄 context(agent 不需自己帶)。
	entries, err := entryStore.ListEntriesByRange(CurrentChannel(), from, to)
	if err != nil {
		return nil, fmt.Errorf("查詢條目失敗: %w", err)
	}

	// 把條目整理成給 LLM 閱讀的文字。
	var sb strings.Builder
	if len(entries) == 0 {
		sb.WriteString("(沒有符合的條目)")
	} else {
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("・[entryID=%s] %s %s\n", e.ID, describeTime(e.Start, e.StartTime, e.End, e.EndTime), e.Item))
		}
	}
	summary := strings.TrimRight(sb.String(), "\n")

	ctx.EmitToolResult(map[string]interface{}{
		"summary": summary,
		"count":   len(entries),
	})
	return []types.ResultContentBlock{types.TextBlock(summary)}, nil
}

func (t *QueryEntriesTool) RenderToolUse(args types.ToolArguments) string {
	from, to := args.GetString("from"), args.GetString("to")
	if from == "" && to == "" {
		return "正在查詢全部條目"
	}
	return fmt.Sprintf("正在查詢條目:%s ~ %s", from, to)
}

func (t *QueryEntriesTool) RenderToolUseError(err error) string {
	return fmt.Sprintf("查詢條目失敗:%v", err)
}

func (t *QueryEntriesTool) RenderToolResult(data map[string]interface{}) string {
	if msg, ok := data["summary"].(string); ok {
		return msg
	}
	return "查詢完成"
}
