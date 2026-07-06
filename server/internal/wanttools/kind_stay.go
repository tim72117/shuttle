package wanttools

import (
	"fmt"

	"github.com/tim72117/want/types"
)

// staySpec 是住宿類型(kind=stay)的欄位規範。
//
// 住宿本質是「一段區間」:入住=條目的 start/startTime、退房=條目的 end/endTime,
// 不另存重複的時間欄位。專屬規則:
//   - 必須有 end(退房日):住宿不能只有單一天,無 end 則擋下要求 LLM 補齊。
//   - startTime 未給 → 預設 15:00(一般 check-in 時刻)。
//   - endTime 未給   → 預設 11:00(一般 check-out 時刻)。
type staySpec struct{}

func (staySpec) Kind() string { return "stay" }

func (staySpec) Validate(args types.ToolArguments) error {
	if args.GetString("start") == "" {
		return fmt.Errorf("kind=stay 需要 start(入住日):住宿是一段區間,請補上入住日期")
	}
	if args.GetString("end") == "" {
		return fmt.Errorf("kind=stay 需要 end(退房日):住宿是一段區間,只給入住日不足,請補上退房日期")
	}
	return nil
}

func (staySpec) ApplyDefaults(args types.ToolArguments) {
	// 時刻未給時補住宿常規時刻,對應 check-in / check-out。
	if args.GetString("startTime") == "" {
		args["startTime"] = "15:00"
	}
	if args.GetString("endTime") == "" {
		args["endTime"] = "11:00"
	}
}

func init() {
	RegisterKind(staySpec{})
}
