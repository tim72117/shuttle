import type { ClientTool } from '../../sdk-proposals/arrayTools'
import { defineTool } from '../../sdk-proposals/defineTool'
import { asNonNegativeInt, type TripBatches } from '../tripEntryTools'

// TripListBatchesCtx — 這個工具實際用到的 context 子集,只有 getAllBatches
// 一個口子(純讀取工具,不改動 allBatches,故不需要 setAllBatches;也不是
// 給 LLM 用的清單查詢通知對象,故不需要 notifyBatchQueried——那是
// tripEntryList 才需要的口子,見 ClientToolsBridge.ts 的 ToolContext 型別
// 說明)。刻意不 import 完整的 ToolContext——這個檔案除了 defineTool/
// ClientTool 這兩個 sdk-proposals 的通用型別以外,不依賴 ClientToolsBridge.ts
// 的任何具體型別,理論上可以整份搬到任何提供「getAllBatches 這一個口子」的
// bridge 底下用。
//
// 這能成立是靠 TypeScript 的結構化型別 + 函式參數逆變:handle 宣告成只需要
// TripListBatchesCtx(較窄的需求),之後仍能被放進要求 ClientTool<ToolContext>
// 的陣列(tools/index.ts 的 defaultClientTools,元素型別是
// ClientToolsBridge.ts 的 BridgeTool = ClientTool<ToolContext>),因為真正
// 呼叫時傳進來的 ToolContext 物件本身就同時滿足這個較窄的需求(多出來的
// setAllBatches/notifyBatchQueried 欄位單純被忽略)——「需要的比較少」的
// 函式,可以放到「會給的比較多」的地方用,這正是介面隔離原則(Interface
// Segregation)的型別層體現。
type TripListBatchesCtx = {
  getAllBatches: () => TripBatches
}

// BatchSummary — 一個批次的摘要,形狀對齊 server/tools/clienttools.yaml
// 裡 trip_list_batches 的 returns 定義(key/count/firstDate/lastDate/
// sampleTitles)。目標是讓 LLM 光看這份摘要就能判斷「這批大概是什麼」、
// 決定要不要沿用這個 key,不需要為此另外呼叫 trip_entry_list 把整批內容
// 都拉回去看。
export type BatchSummary = {
  key: string
  count: number
  firstDate: string
  lastDate: string
  sampleTitles: string[]
}

const MAX_SAMPLE_TITLES = 3

// summarizeBatch：單一批次的摘要邏輯。firstDate/lastDate 用「日期字串排序
// 後取頭尾」而非「依插入順序取頭尾」——entries 是依新增順序排列,不保證
// date 欄位本身有序(使用者可能先記 7/3 的行程,晚點才補記 7/1 的),排序後
// 取頭尾才能反映真正的日期範圍。日期字串為 'YYYY-MM-DD' 格式,字典序排序
// 等同時間序,不需要额外解析成 Date。空字串(未填日期)排序時會排最前面,
// 若整批都沒有日期,firstDate/lastDate 會回傳空字串——這正是「沒有日期
// 範圍可言」的正確表示,不需要額外過濾。
function summarizeBatch(key: string, entries: TripBatches[string]): BatchSummary {
  const dates = entries.map((e) => e.date).sort()
  return {
    key,
    count: entries.length,
    firstDate: dates.length > 0 ? dates[0] : '',
    lastDate: dates.length > 0 ? dates[dates.length - 1] : '',
    sampleTitles: entries.slice(0, MAX_SAMPLE_TITLES).map((e) => e.title),
  }
}

// listTripBatches：分頁列出目前所有批次的摘要,純邏輯,不含 React 依賴,不
// 改動 allBatches。分頁邏輯比照 listTripEntries(見 ./tripEntryList.ts)——
// 先算出完整的批次摘要陣列,再依 offset/limit slice。offset/limit 在
// server/tools/clienttools.yaml 標成必填,但這裡仍防禦性地處理「缺漏或型別
// 不可信」的情況——offset 缺漏或無效值一律回退到 0(從頭查);limit 缺漏或
// 無效值(含 0、負數)一律回退到批次總數(等同「這次查全部」,也避免
// limit<=0 時 slice 出空陣列讓 LLM 誤以為批次是空的)。offset 超出批次數量
// 時 slice 自然回傳空陣列。維持 export 讓這段邏輯可以被單獨測試。
export function listTripBatches(
  allBatches: TripBatches,
  args: Record<string, unknown>,
): { result: { batches: BatchSummary[]; total: number } } {
  const batches = Object.keys(allBatches).map((key) => summarizeBatch(key, allBatches[key]))
  const total = batches.length
  const offset = asNonNegativeInt(args.offset, 0)
  const rawLimit = asNonNegativeInt(args.limit, total)
  const limit = rawLimit > 0 ? rawLimit : total
  return { result: { batches: batches.slice(offset, offset + limit), total } }
}

// TripListBatchesArgs — trip_list_batches 的 args 型別,對齊 server/tools/
// clienttools.yaml 的 parameters schema(offset/limit 皆必填)。跟
// TripEntryListArgs(見 tripEntryList.ts)同樣的取捨:offset/limit 仍宣告
// 成 unknown,「防禦性轉型」留給 listTripBatches 內部的 asNonNegativeInt
// 處理(LLM 實際傳回來的數字參數不保證是原生 number)。
type TripListBatchesArgs = {
  offset: unknown
  limit: unknown
}

function parseTripListBatchesArgs(raw: unknown): TripListBatchesArgs {
  const r = (raw ?? {}) as Record<string, unknown>
  return { offset: r.offset, limit: r.limit }
}

// tripListBatches — trip_list_batches 工具宣告,用 defineTool 包裝(見
// sdk-proposals/defineTool.ts 的設計說明)。純讀取、不改動 allBatches,所以
// 不需要呼叫 ctx.setAllBatches。
export const tripListBatches: ClientTool<TripListBatchesCtx> = defineTool(
  'trip_list_batches',
  parseTripListBatchesArgs,
  (args, ctx) => {
    return listTripBatches(ctx.getAllBatches(), args).result
  },
)
