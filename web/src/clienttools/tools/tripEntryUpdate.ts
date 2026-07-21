import type { ClientTool, ToolContext } from '../ClientToolsBridge'
import { asString, type TripBatches } from '../tripEntryTools'

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

// tripEntryUpdate — trip_entry_update 工具宣告。找不到對應 key/id 時
// updateTripEntry 會 throw,交給 bridge 統一轉成 tool_result 的 error 回應。
export const tripEntryUpdate: ClientTool = {
  name: 'trip_entry_update',
  handle: (args, ctx: ToolContext) => {
    const key = asString(args.key)
    const { allBatches: next, result } = updateTripEntry(ctx.getAllBatches(), key, args)
    ctx.setAllBatches(next)
    return result
  },
}
