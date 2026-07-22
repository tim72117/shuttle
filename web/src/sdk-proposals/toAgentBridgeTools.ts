// toAgentBridgeTools — 試做:模擬「如果 @onagent/bridge 這個 SDK 願意補上
// 陣列註冊功能」會長什麼樣。這個檔案本身不是改 SDK(node_modules 裡的套件
// 不能改、也不該改),而是在這個專案自己的程式碼裡先做一版轉接層,驗證這個
// 提案是否可行、值不值得回饋給 SDK 作者(見這個目錄 sdk-proposals/ 的整體
// 定位說明,defineTool.ts 開頭有更完整的敘述)。
//
// 動機:AgentBridge 建構子(見 @onagent/bridge 的 client.ts)要求
// `tools: Record<string, ToolHandler>`——已經是組好的表,不接受陣列。這逼
// 每個消費者自己手寫轉換邏輯(見 ../OnagentBridgeDemo.tsx 原本手動列舉
// trip_entry_add/trip_entry_list 兩行的寫法),且完全沒有「重複名稱」防呆
// ——如果不小心把兩個同名工具塞進同一個 Record 字面量,TypeScript 不會報錯
// (物件字面量後面的 key 覆蓋前面的是合法語法),只會悄悄地讓前一個工具失聯,
// 很難 debug。
//
// 對照組:這個專案自己的 ClientToolsBridge 建構子(見 ../clienttools/
// ClientToolsBridge.ts)直接吃 BridgeTool[] 陣列,內部組表時明確檢查重複
// 名稱、找到就直接 throw Error 讓開發者馬上發現(見該檔案 constructor 的
// 說明)。toAgentBridgeTools 把同樣的模式搬過來套用在 AgentBridge 身上。
//
// 用法:defineTool 產出的 ClientTool 物件可以直接放進陣列,呼叫
// toAgentBridgeTools(tools, ctx) 轉成 AgentBridgeOptions.tools 要的形狀:
//
//   new AgentBridge({
//     ...,
//     tools: toAgentBridgeTools([tripEntryAdd, tripEntryList], onagentToolContext),
//   })
//
// 不需要再像原本那樣為每個工具手寫一行轉接程式碼,新增工具只需要加進陣列。

import type { ClientTool } from './arrayTools'

// AgentBridge 的 ToolHandler 型別(@onagent/bridge 沒有 export 給外部直接
// import 這個型別名稱的簡便方式,故在這裡照它的公開簽章重新宣告一份對齊
// ——(args: any) => Promise<unknown> | unknown,寫成 unknown 而非 any,
// 呼叫端內部仍會轉呼叫 ClientTool.handle,型別安全由 ClientTool 那一側
// 保證,這裡只是符合 AgentBridge 要求的外層簽章)。
type AgentBridgeToolHandler = (args: Record<string, unknown>) => unknown

// toAgentBridgeTools — 把 ClientTool<Ctx>[] 轉成 AgentBridgeOptions.tools 要
// 的 Record<string, ToolHandler> 形狀,統一注入呼叫端提供的同一個 ctx(Ctx
// 由呼叫端自己決定型別——這裡不寫死成 tripace 的 ToolContext,SDK 原生
// ToolHandler 完全不帶 context,是否需要 ctx、ctx 長怎樣,都是消費者的選擇,
// 不是這個轉接層該預設的事)。
//
// 重複名稱防呆:同一批 tools 若有重複的 name,直接丟出 Error,不讓後面的
// 悄悄覆蓋前面的——同 ClientToolsBridge constructor 的既有慣例,理由相同
// (悄悄覆蓋會讓某個工具的呼叫默默失聯,很難 debug)。
//
// onToolResult(選用):每次工具執行成功後回報 { name, args, result },供呼叫端
// 接自己的 log/UI(例如 OnagentBridgeDemo.tsx 想在畫面上顯示「哪個工具被
// 呼叫、結果是什麼」)。整批轉換後個別工具呼叫點消失了(不再像手動列舉時
// 那樣,每個工具各自一行 pushLog),用這個回呼補回同等的可觀測性,而不是
// 要求呼叫端另外包一層才能看到執行紀錄。錯誤(handle 拋出例外)不吞、不
// 過這個回呼——直接往外 throw,交給 AgentBridge 既有的 try/catch(見
// handleToolCall)轉成 tool_result 的 { ok: false, error } 回報,同
// defineTool 的既有取捨(見該檔案的說明),不重新發明錯誤處理路徑。
export function toAgentBridgeTools<Ctx>(
  tools: ClientTool<Ctx>[],
  ctx: Ctx,
  onToolResult?: (info: { name: string; args: Record<string, unknown>; result: unknown }) => void,
): Record<string, AgentBridgeToolHandler> {
  const result: Record<string, AgentBridgeToolHandler> = {}
  for (const tool of tools) {
    if (Object.prototype.hasOwnProperty.call(result, tool.name)) {
      throw new Error(`toAgentBridgeTools: duplicate tool name "${tool.name}"`)
    }
    result[tool.name] = (args) => {
      const toolResult = tool.handle(args, ctx)
      onToolResult?.({ name: tool.name, args, result: toolResult })
      return toolResult
    }
  }
  return result
}
