import type { ClientTool, ToolContext } from '../ClientToolsBridge'
import { asNonNegativeInt, asString, type TripBatches, type TripEntry } from '../tripEntryTools'

// listTripEntries：分頁查詢某一批(key)的純邏輯,不改動 allBatches,純讀取,
// 不含 React 依賴。key 對應的批次不存在時視為空清單(total=0、entries=[]）
// ——LLM 查詢一個尚未新增過任何項目的 key 是合理情境(例如剛從
// trip_list_batches 得知某 key 存在但這批目前是空的,或誤用了不存在的
// key),不視為錯誤、不 throw,直接回報空結果讓 LLM 自行判斷。offset/limit
// 在 server/tools/clienttools.yaml 標成必填,但這裡仍防禦性地處理「缺漏或
// 型別不可信」的情況——offset 缺漏或無效值一律回退到 0(從頭查);limit
// 缺漏或無效值(含 0、負數)一律回退到該批清單長度(等同「這次查全部」,也
// 避免 limit<=0 時 slice 出空陣列讓 LLM 誤以為清單是空的)。offset 超出清單
// 長度時 slice 自然回傳空陣列。asNonNegativeInt 是共用的非負整數轉型
// helper(留在 tripEntryTools.ts,不是這個工具專屬邏輯)。維持 export 讓這段
// 邏輯可以被單獨測試或未來獨立復用。
export function listTripEntries(
  allBatches: TripBatches,
  key: string,
  args: Record<string, unknown>,
): { result: { entries: TripEntry[]; total: number } } {
  const entries = allBatches[key] ?? []
  const total = entries.length
  const offset = asNonNegativeInt(args.offset, 0)
  const rawLimit = asNonNegativeInt(args.limit, total)
  const limit = rawLimit > 0 ? rawLimit : total
  return { result: { entries: entries.slice(offset, offset + limit), total } }
}

// tripEntryList — trip_entry_list 工具宣告。純讀取、不改動 allBatches,所以
// 不需要呼叫 ctx.setAllBatches;但仍需讓呼叫端(ChatScreen.tsx)知道「這個 key
// 剛被查詢過」,才能在答案訊息底下顯示對應的清單——「內容比對」機制
// (ChatScreen.tsx 的 changedBatchKeys)對純讀取工具永遠偵測不到變化,故改用
// ctx.notifyBatchQueried 這個平行、獨立的通知口子主動回報(見 ClientToolsBridge.ts
// ToolContext 型別定義處的完整說明)。
export const tripEntryList: ClientTool = {
  name: 'trip_entry_list',
  handle: (args, ctx: ToolContext) => {
    const key = asString(args.key)
    const result = listTripEntries(ctx.getAllBatches(), key, args).result
    ctx.notifyBatchQueried(key)
    return result
  },
}
