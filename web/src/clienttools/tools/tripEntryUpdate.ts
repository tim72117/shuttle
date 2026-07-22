import type { ClientTool } from '../../sdk-proposals/arrayTools'
import { defineTool } from '../../sdk-proposals/defineTool'
import { asString, type TripBatches } from '../tripEntryTools'

// TripEntryUpdateCtx — 這個工具實際用到的 context 子集,只有 getAllBatches/
// setAllBatches 兩個口子(不用 notifyBatchQueried,那是 tripEntryList 才需要
// 的通知口子,見 ClientToolsBridge.ts 的 ToolContext 型別說明)。刻意不 import
// 完整的 ToolContext,理由與 tripEntryAdd.ts 的 TripEntryAddCtx 完全相同:
// 這個檔案除了 defineTool/ClientTool 這兩個 sdk-proposals 的通用型別以外,
// 不依賴 ClientToolsBridge.ts 的任何具體型別;之所以仍能被放進要求
// ClientTool<ToolContext>(即 BridgeTool)的陣列(tools/index.ts 的
// defaultClientTools),是靠 TypeScript 結構化型別 + 函式參數逆變——真正
// 呼叫時傳進來的 ToolContext 物件本身就同時滿足這個較窄的需求(多出來的
// notifyBatchQueried 欄位單純被忽略)。
type TripEntryUpdateCtx = {
  getAllBatches: () => TripBatches
  setAllBatches: (next: TripBatches) => void
}

// updateTripEntry：修改一筆旅程 entry 的純邏輯,不含 React 依賴。找不到對應
// key/id 判斷的注意事項同 tripEntryDelete.ts 的 deleteTripEntry(呼叫端必須
// 傳入「當下已確定穩定」的 allBatches 快照,避免同一輪推論連續呼叫多個工具時
// 的競態)。只傳入要改的欄位,其餘留空字串表示不修改;找不到時 throw。維持
// export 讓這段邏輯可以被單獨測試或未來獨立復用。
export function updateTripEntry(
  allBatches: TripBatches,
  key: string,
  args: Record<string, unknown>,
): { allBatches: TripBatches; result: { updated: string } } {
  const id = asString(args.id)
  const entries = allBatches[key]
  if (!entries || !entries.some((e) => e.id === id)) {
    throw new Error(`entry ${id} not found in batch ${key}`)
  }
  const next = entries.map((e) => {
    if (e.id !== id) return e
    return {
      ...e,
      title: asString(args.title) || e.title,
      date: asString(args.date) || e.date,
      time: args.time !== undefined && asString(args.time) !== '' ? asString(args.time) : e.time,
      note: args.note !== undefined && asString(args.note) !== '' ? asString(args.note) : e.note,
    }
  })
  const nextAllBatches: TripBatches = { ...allBatches, [key]: next }
  return { allBatches: nextAllBatches, result: { updated: id } }
}

// TripEntryUpdateArgs — trip_entry_update 的 args 型別,對齊 server/tools/
// clienttools.yaml 的 parameters schema(key、id 必填,title/date/time/note
// 皆選填)。time/note 刻意保留 unknown(而非轉成 string)——updateTripEntry
// 內部用 `args.time !== undefined` 判斷「這個欄位有沒有被傳」跟「被傳成
// 空字串」是不同語意(只傳其中幾個欄位表示「其餘不修改」,見該函式的說明),
// parseArgs 若把 undefined 也轉成空字串,會讓這個判斷永遠為 true、改變既有
// 行為,故這兩個欄位不能用 asString 統一轉型,原樣傳遞給 updateTripEntry
// 內部處理。
type TripEntryUpdateArgs = {
  key: string
  id: string
  title?: string
  date?: string
  time: unknown
  note: unknown
}

function parseTripEntryUpdateArgs(raw: unknown): TripEntryUpdateArgs {
  const r = (raw ?? {}) as Record<string, unknown>
  return {
    key: asString(r.key),
    id: asString(r.id),
    title: asString(r.title) || undefined,
    date: asString(r.date) || undefined,
    time: r.time,
    note: r.note,
  }
}

// tripEntryUpdate — trip_entry_update 工具宣告,用 defineTool 包裝(見
// sdk-proposals/defineTool.ts 的設計說明)。找不到對應 key/id 時
// updateTripEntry 會 throw,交給 bridge 統一轉成 tool_result 的 error 回應。
export const tripEntryUpdate: ClientTool<TripEntryUpdateCtx> = defineTool(
  'trip_entry_update',
  parseTripEntryUpdateArgs,
  (args, ctx) => {
    const { allBatches: next, result } = updateTripEntry(ctx.getAllBatches(), args.key, args)
    ctx.setAllBatches(next)
    return result
  },
)
