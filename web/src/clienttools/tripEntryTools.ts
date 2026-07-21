// tripEntryTools — trip_entry_* 系列工具共用的型別與轉型 helper。每個工具
// 自身的邏輯(新增/刪除/修改/查詢)已搬進 ./tools/ 底下對應的檔案,跟各自的
// ClientTool 宣告放在同一處(工具的設定與邏輯封裝在一起);這裡只留給多個
// 工具共用、不屬於任何單一工具的部分——不含任何 React/UI 依賴。

export type TripEntry = {
  id: string
  title: string
  date: string
  time: string
  note: string
}

// TripBatches — 多批次旅程清單的資料形狀:key(批次識別字串)對應到該批的
// TripEntry 陣列。不用 Map——這份資料要能直接當 React state(見
// web/src/ChatScreen.tsx 的 clientToolsEntries),Map 在 React 的
// immutability 慣例下每次更新都要手動 new Map(prev) 再逐一搬值,比一般物件
// + 展開運算子({ ...prev, [key]: next })繁瑣許多,而這裡的操作(讀/寫單一
// key、列出所有 key)一般物件都能簡單勝任,不需要 Map 的排序保證或非字串鍵
// 等特性。
export type TripBatches = Record<string, TripEntry[]>

export function asString(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

// asNonNegativeInt：目前只有 listTripEntries(見 ./tools/tripEntryList.ts)
// 用到,但它的用途是通用的非負整數轉型,不是任何單一工具的專屬業務邏輯,
// 因此留在這裡而非搬進 tripEntryList.ts。LLM 透過 want/vLLM 傳回來的數字
// 參數,實測不保證一定是原生 JS number——JSON 走過一輪 provider 的
// tool-call 解析後,常見還是 number,但也可能被序列化成 numeric string
// (例如 "0"),甚至偶爾整段參數 JSON 解析失敗掉回字串。用 Number() 統一
// 轉換(對 number 是 no-op,對 numeric string 能轉,對非數字字串/undefined/
// NaN 都會得到 NaN),NaN 與負值一律回退到 fallback。
export function asNonNegativeInt(v: unknown, fallback: number): number {
  const n = Math.trunc(Number(v))
  return Number.isFinite(n) && n >= 0 ? n : fallback
}
