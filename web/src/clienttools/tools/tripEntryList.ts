import type { ClientTool } from '../../sdk-proposals/arrayTools'
import { defineTool } from '../../sdk-proposals/defineTool'
import { asNonNegativeInt, asString, type TripBatches, type TripEntry } from '../tripEntryTools'

// TripEntryListCtx — 這個工具實際用到的 context 子集,只有 getAllBatches/
// notifyBatchQueried 兩個口子。不含 setAllBatches——這是純讀取工具,不改動
// allBatches,不需要寫回的口子;它需要的是另一個平行、獨立的通知口子
// notifyBatchQueried,主動回報「這個 key 剛被查詢過」(見 ClientToolsBridge.ts
// 的 ToolContext 型別說明)。刻意不 import 完整的 ToolContext——這個檔案除了
// defineTool/ClientTool 這兩個 sdk-proposals 的通用型別以外,不依賴
// ClientToolsBridge.ts 的任何具體型別,理論上可以整份搬到任何提供
// 「getAllBatches/notifyBatchQueried 這兩個口子」的 bridge 底下用。
//
// 這能成立同樣是靠 TypeScript 的結構化型別 + 函式參數逆變(見
// tripEntryAdd.ts 的 TripEntryAddCtx 說明):handle 宣告成只需要
// TripEntryListCtx(較窄的需求),之後仍能被放進要求 ClientTool<ToolContext>
// 的陣列(tools/index.ts 的 defaultClientTools,元素型別是
// ClientToolsBridge.ts 的 BridgeTool = ClientTool<ToolContext>),因為真正
// 呼叫時傳進來的 ToolContext 物件本身就同時滿足這個較窄的需求(多出來的
// setAllBatches 欄位單純被忽略)。
type TripEntryListCtx = {
  getAllBatches: () => TripBatches
  notifyBatchQueried: (key: string) => void
}

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

// TripEntryListArgs — trip_entry_list 的 args 型別,對齊 server/tools/
// clienttools.yaml 的 parameters schema(key/offset/limit 皆必填,但這裡
// offset/limit 仍宣告成可能是任意 unknown 值——LLM 實際傳回來的數字參數
// 不保證是原生 number,見 tripEntryTools.ts 的 asNonNegativeInt 說明;把
// 「防禦性轉型」留給 listTripEntries 內部的 asNonNegativeInt 處理,這裡
// 只確保 key 一定是 string)。
type TripEntryListArgs = {
  key: string
  offset: unknown
  limit: unknown
}

function parseTripEntryListArgs(raw: unknown): TripEntryListArgs {
  const r = (raw ?? {}) as Record<string, unknown>
  return { key: asString(r.key), offset: r.offset, limit: r.limit }
}

// tripEntryList — trip_entry_list 工具宣告,用 defineTool 包裝(見 defineTool.ts
// 的設計說明)。純讀取、不改動 allBatches,所以不需要呼叫 ctx.setAllBatches;
// 但仍需讓呼叫端(ChatScreen.tsx)知道「這個 key 剛被查詢過」,才能在答案訊息
// 底下顯示對應的清單——「內容比對」機制(ChatScreen.tsx 的 changedBatchKeys)
// 對純讀取工具永遠偵測不到變化,故改用 ctx.notifyBatchQueried 這個平行、獨立
// 的通知口子主動回報(見 ClientToolsBridge.ts ToolContext 型別定義處的完整
// 說明)。
export const tripEntryList: ClientTool<TripEntryListCtx> = defineTool(
  'trip_entry_list',
  parseTripEntryListArgs,
  (args, ctx) => {
    const result = listTripEntries(ctx.getAllBatches(), args.key, args).result
    ctx.notifyBatchQueried(args.key)
    return result
  },
)
