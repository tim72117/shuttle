// arrayTools — 試做:AgentBridge 建構子若願意多接受一種「工具陣列」輸入
// (而不是只收組好的 Record<string, ToolHandler>),核心邏輯會長什麼樣。
//
// 跟 toAgentBridgeTools.ts 的差異:toAgentBridgeTools 是「不改 SDK,在這個
// 專案自己的程式碼裡做一層轉接」——外部 adapter,SDK 本身完全不用動。這個
// 檔案反過來,示範的是「SDK 內部真的採納這個模式,建構子本身長怎樣」,是
// 更進一步、真正可以貼給 SDK 作者看的最小提案:只留「陣列 → 查表,重複
// 就 throw」這一段核心邏輯,不帶任何 tripace 專案私有的擴充。
//
// 這個檔案是 sdk-proposals/ 目錄裡型別定義的唯一自足來源:defineTool.ts、
// toAgentBridgeTools.ts 都改成從這裡 import ClientTool,不再向
// ../clienttools/ClientToolsBridge.ts 借用型別——那兩個檔案的存在本身依賴
// ClientToolsBridge.ts,任何只想用 sdk-proposals、完全不碰
// ClientToolsBridge.ts 的新元件都會被那個 import 卡住,「理論上可以抽離出
// 這個 repo」這句話就不成立。方向必須反過來:sdk-proposals 自己定義好一套
// 完整型別,clienttools/ClientToolsBridge.ts 的 ClientTool 改成基於這裡的
// 型別做具體實例化(見該檔案的型別定義處的說明),而不是現在這樣反過來被
// 借用。
//
// 命名沿用 ClientTool 這個既有名字(而非另創一個「SDKTool」之類的新詞彙)
// ——這裡定義的本來就是 tripace 這個專案 ClientTool 概念的通用化版本,用
// 同一個名字才看得出兩者是同一件事,不是兩套平行概念。ClientToolsBridge.ts
// 的 ClientTool 需要 import 這裡的泛型型別再具體代入 ToolContext,兩個
// 檔案裡都會出現「ClientTool」這個名字,若有撞名疑慮,由匯入端自行取別名
// 即可(見 ClientToolsBridge.ts 的 import 寫法)。
//
// Ctx 泛型參數:不是每個消費者都需要「context」這個概念(SDK 原生
// ToolHandler 就完全不帶),但也不能寫死「一定不帶」——tripace 這個專案的
// ClientTool 就需要帶 ToolContext 才能運作(讀寫 allBatches)。用泛型
// ClientTool<Ctx> 表達這件事:Ctx 預設是 void(對齊 SDK 現有 ToolHandler
// 簽章,handle 只收 args),消費者需要 context 時自己代入實際型別,handle
// 簽章自動變成收兩個參數。這樣「要不要帶 context」變成消費者自己的選擇,
// 不是 sdk-proposals 替所有消費者預先決定的事——ClientToolsBridge.ts 的
// ClientTool 正是這個泛型型別代入 ToolContext 後的具體實例化(見該檔案的
// 型別定義)。
export type ClientTool<Ctx = void> = {
  name: string
  handle: Ctx extends void
    ? (args: Record<string, unknown>) => unknown
    : (args: Record<string, unknown>, ctx: Ctx) => unknown
}

// ClientToolHandler——單獨 export 出來,給 defineTool.ts 的 handle 參數型別、
// ClientToolsBridge.ts 組 handlers 表時的內部型別共用,不必各自重新推導
// ClientTool<Ctx>['handle'] 這種寫法。
export type ClientToolHandler<Ctx = void> = ClientTool<Ctx>['handle']

// toToolRecord——把 ClientTool<Ctx>[] 轉成 Record<string, handler> 形狀(Ctx
// 為 void 時就是 AgentBridge 建構子的 tools 選項要的 Record<string,
// ToolHandler>)。重複名稱視為設定錯誤,直接丟出 Error 讓開發者馬上發現,
// 不要讓後面的悄悄覆蓋前面的——物件字面量寫法(`{ ...a, ...b }`)後面的 key
// 覆蓋前面是合法語法,TypeScript 不會報錯,只會讓某個工具的呼叫默默失聯,
// 很難 debug。
export function toToolRecord<Ctx = void>(
  tools: ClientTool<Ctx>[],
): Record<string, ClientToolHandler<Ctx>> {
  const result: Record<string, ClientToolHandler<Ctx>> = {}
  for (const tool of tools) {
    if (Object.prototype.hasOwnProperty.call(result, tool.name)) {
      throw new Error(`toToolRecord: duplicate tool name "${tool.name}"`)
    }
    result[tool.name] = tool.handle
  }
  return result
}
