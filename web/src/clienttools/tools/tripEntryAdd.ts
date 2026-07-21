import type { ClientTool, ToolContext } from '../ClientToolsBridge'
import { asString, type TripBatches, type TripEntry } from '../tripEntryTools'

// newTripEntryId：核心用途是 trip_entry_add 產生新 id,因此邏輯搬進來跟工具
// 宣告放在同一個檔案;但 export 出去是因為 ClientToolsDemo.tsx 建立初始
// 示範資料時也需要產生 id(不經過這個工具的 handle,是畫面掛載時的種子資料),
// 所以不算「完全只被這個工具內部使用」,不能設為模組私有函式。
export function newTripEntryId(): string {
  return 'trip_' + Math.random().toString(36).slice(2, 10)
}

// addTripEntry：新增一筆旅程 entry 的純邏輯,不含 React 依賴。輸入目前的
// allBatches(所有批次)+ 要操作的 key + LLM 傳來的 args,回傳更新後的整個
// allBatches 與要回報給 LLM 的 result。key 對應的批次若原本不存在,視為
// LLM 開了一個新批次,直接建立(見 server/tools/clienttools.yaml「多批次
// (key)支援」——key 由 LLM 自訂字串,不要求前端事先存在才能新增)。維持
// export 讓這段邏輯可以被單獨測試,或未來被其他前端工具(例如非 WS bridge
// 的手動編輯 UI)獨立復用,不需要經過 ClientTool/ToolContext 這層協定包裝。
export function addTripEntry(
  allBatches: TripBatches,
  key: string,
  args: Record<string, unknown>,
): { allBatches: TripBatches; result: { id: string; key: string; title: string; date: string } } {
  const entry: TripEntry = {
    id: newTripEntryId(),
    title: asString(args.title) || '(未命名項目)',
    date: asString(args.date),
    time: asString(args.time),
    note: asString(args.note),
  }
  const existing = allBatches[key] ?? []
  const nextAllBatches: TripBatches = { ...allBatches, [key]: [...existing, entry] }
  return { allBatches: nextAllBatches, result: { id: entry.id, key, title: entry.title, date: entry.date } }
}

// tripEntryAdd — trip_entry_add 工具宣告。這裡只負責接線:透過 ctx 讀當下
// allBatches、把純函式回傳的新 allBatches 寫回 ctx、回傳 result 給 bridge
// 送回 LLM。key 是必填參數(見 clienttools.yaml),缺漏時視為空字串批次
// (asString 對非字串一律回傳空字串)——空字串本身也是一個合法的 key,不
// 特別擋下,讓 bridge 層保持單純,異常輸入的處理交給呼叫端(LLM)的工具
// schema 必填驗證負責。
export const tripEntryAdd: ClientTool = {
  name: 'trip_entry_add',
  handle: (args, ctx: ToolContext) => {
    const key = asString(args.key)
    const { allBatches: next, result } = addTripEntry(ctx.getAllBatches(), key, args)
    ctx.setAllBatches(next)
    return result
  },
}
