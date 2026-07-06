package wanttools

import "github.com/tim72117/want/types"

// KindSpec 是「條目類型（kind）專屬欄位規範」的策略介面。
//
// 不同類型的條目(住宿 stay、航班 flight、餐飲 restaurant…)有各自的欄位要求
// 與預設值。每個類型實作一份 KindSpec,註冊到 kindRegistry;entry_add 不需知道
// 任何特定類型的細節,只查表委派——這與本套件既有的 types.RegisterTool 模式一致。
//
// 擴充新類型:新增一個 kind_<name>.go,實作 KindSpec,並在其 init() 呼叫 RegisterKind。
// entry_add.go 本身不需改動。
type KindSpec interface {
	// Kind 回傳此策略對應的 kind 字串(如 "stay")。
	Kind() string
	// Validate 檢查該類型專屬的欄位要求;不符則回 error(entry_add 會擋下並讓 LLM 補齊)。
	// 通用欄位(如 item)由 entry_add 自行檢查,這裡只管該類型專屬規則。
	Validate(args types.ToolArguments) error
	// ApplyDefaults 就地補上該類型的預設值(如 stay 未給時刻時補 check-in/out 預設時刻)。
	// args 是可變的 map,直接寫回即可。在 Validate 通過後、實際寫入前呼叫。
	ApplyDefaults(args types.ToolArguments)
}

// kindRegistry:kind 字串 → 對應策略。由各 kind_*.go 的 init() 透過 RegisterKind 填入。
var kindRegistry = map[string]KindSpec{}

// RegisterKind 註冊一個類型策略(供各 kind_*.go 的 init() 呼叫)。
func RegisterKind(s KindSpec) { kindRegistry[s.Kind()] = s }

// lookupKind 取出對應 kind 的策略;無 kind 或未知 kind 時回 (nil, false)。
func lookupKind(kind string) (KindSpec, bool) {
	if kind == "" {
		return nil, false
	}
	s, ok := kindRegistry[kind]
	return s, ok
}

// validateKind 對指定 kind 套用專屬驗證。無 kind 或未知 kind 時視為通過
// (未知 kind 不擋:允許 LLM 填尚未定義策略的類型,只是不做專屬檢查)。
func validateKind(args types.ToolArguments) error {
	if s, ok := lookupKind(args.GetString("kind")); ok {
		return s.Validate(args)
	}
	return nil
}

// applyKindDefaults 對指定 kind 套用專屬預設值。無 kind 或未知 kind 時不動 args。
func applyKindDefaults(args types.ToolArguments) {
	if s, ok := lookupKind(args.GetString("kind")); ok {
		s.ApplyDefaults(args)
	}
}
