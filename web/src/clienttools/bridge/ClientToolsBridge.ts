import { BASE_URL } from '../../App'
import type { TripBatches } from '../tripEntryTools'
import { toToolRecord, type ClientTool, type ClientToolHandler } from '../../sdk-proposals/arrayTools'

// ClientToolsBridge — 「LLM 呼叫前端 tool」試做(POC)的連線與協定邏輯,從
// ClientToolsDemo.tsx 拆出來,不含任何 React 依賴(不 import React、不用
// useState/useRef/useEffect),是一個普通 TypeScript class,方便未來被非
// React 環境或其他元件復用。
//
// 旅程清單可能同時存在多批(各自用一個 key 字串識別,見 tripEntryTools.ts
// 的 TripBatches 型別),只存在這個 class 的私有欄位(不進任何後端資料庫,
// 重新整理頁面就會消失)。使用者打一句話送給後端,後端 want agent(server/
// internal/llm 的獨立 clienttools agent role,見 clienttools_agent.go)決定
// 要呼叫 trip_entry_add / trip_entry_delete / trip_entry_update /
// trip_entry_list / trip_list_batches 中的哪一個,透過 WebSocket 把呼叫轉發
// 回這個頁面;下面的 bridge 收到後執行對應的前端函式(改這個 class 的私有
// allBatches 欄位),再把結果送回去。
//
// bridge 邏輯是 /Users/caitingyu/Documents/agent 專案
// packages/bridge/src/client.ts(AgentBridge class)的簡化版本:同樣是「連線 →
// hello → 收 tool_call/tool_query → 查 handler 表 → 執行 → 回 tool_result」,
// 拿掉了正式 SDK 的 reconnect backoff、beacon 離線回報、quota 錯誤處理等 —
// 這是試做,不需要那些正式產品才要的健壯度,但「查表拿 handler、真的執行、真的
// 把結果送回去」這個核心流程原封不動照做。
//
// 後端這邊的協定定義在 server/internal/protocol/message.go(Envelope/
// MessageType),阻塞機制在 server/internal/api/clienttools_ws.go
// (pendingCalls + AskInteraction),都是 agent 專案對應檔案的直接移植。

// ---- wire types(對齊 server/internal/protocol/message.go 的 JSON 形狀) ----

type MessageType =
  | 'hello'
  | 'prompt'
  | 'tool_result'
  | 'ack'
  | 'tool_call'
  | 'tool_query'
  | 'assistant_message'
  | 'error'

type Envelope = {
  type: MessageType
  requestId?: string
  payload?: unknown
}

type ToolCallPayload = {
  toolName: string
  args: Record<string, unknown>
}

type AckPayload = {
  sessionId: string
  toolNames: string[]
}

type AssistantMessagePayload = {
  text: string
}

type ErrorPayload = {
  message: string
}

// ---- 前端工具 handler:直接操作 allBatches,不呼叫任何後端 API ----
//
// 回傳值的形狀對齊 server/tools/clienttools.yaml 各工具的 returns 欄位定義:
// trip_entry_add/trip_entry_list 是 query 型,回傳值會真的被送回 LLM 的推論
// context;trip_entry_delete/trip_entry_update 是 action 型,回傳值只用來判斷
// 「有沒有成功」,LLM 看不到實際內容。

// ToolContext — bridge 傳給每個工具的「有限權限接口」,而不是把整個 bridge
// 實例交出去。工具只能透過這裡明確開放的讀寫口子操作 allBatches 狀態,不會
// 碰到 bridge 的其他私有欄位(WS 連線、log、callbacks…)。這維持工具與 bridge
// 之間清楚的邊界:bridge 不需要知道工具怎麼操作 allBatches,工具也不會退化
// 成互相亂改 bridge 內部狀態。
//
// getEntries/setEntries(單一清單)已改成 getAllBatches/setAllBatches(多批次
// ——見 tripEntryTools.ts 的 TripBatches 型別說明):旅程清單不再是單一一份,
// 而是可能同時存在多批獨立清單,各自用 key 字串識別(server/tools/
// clienttools.yaml「多批次(key)支援」)。個別工具(見 ./tools/ 目錄)各自
// 從 args.key 取出要操作的批次,再對 allBatches[key] 做讀寫——「操作哪個
// key」的判斷與驗證邏輯留在各工具檔案裡,ToolContext 本身只負責整個
// allBatches 物件的讀寫口子,不預設任何單一 key 的存在。
// notifyBatchQueried：平行於 setAllBatches 的另一個獨立通知口子,給純讀取類
// 工具(目前只有 trip_entry_list)用——這類工具不改動 allBatches 內容,呼叫端
// (ChatScreen.tsx)原本用「呼叫前後 allBatches 內容是否不同」判斷這一輪動到了
// 哪些 key(見 changedBatchKeys),但純讀取不會讓內容變化,這個比對機制對它
// 完全失效。這裡不是把「查詢」也塞進 setAllBatches(那會變成「用同一份內容去
// setAllBatches,製造一次沒有實質變化的寫入」,語意錯誤且會讓 setAllBatches
// 的呼叫點失去「這次呼叫代表內容真的變了」的單一意義);而是新增一個語意
// 獨立的方法,讓工具主動回報「我剛查詢了這個 key」,不涉及 allBatches 本身
// 的讀寫。比照 setAllBatches 的既有慣例(工具主動呼叫 ctx 提供的口子,而不是
// bridge 內部依 tool.name === 'trip_entry_list' 做特殊判斷)——bridge 建構子
// 的既有設計原則是「不寫死認得任何具體工具」(見下方 constructor 的說明),
// 若在 bridge 內部特判工具名稱,會破壞這個原則,且未來若有第二個查詢類工具
// 需要同樣的通知,又得再加一次特判;讓工具自己呼叫這個口子,新增工具只需要
// 在工具檔案裡呼叫,bridge 本身完全不需要改動。
export type ToolContext = {
  getAllBatches: () => TripBatches
  setAllBatches: (next: TripBatches) => void
  notifyBatchQueried: (key: string) => void
}

// BridgeTool — 一個工具的完整宣告:name 對應 server/tools/clienttools.yaml
// 裡的 tool name,handle 是實際執行邏輯,多收一個 ctx 參數(見 ToolContext)。
// 工具檔案(見 ./tools/ 目錄)各自 export 一個 BridgeTool 常數,由主程式的
// import 清單決定要餵給 bridge 哪些工具——bridge 本身不再寫死認得任何具體
// 工具。
//
// 型別上是 sdk-proposals/arrayTools.ts 的 ClientTool<Ctx> 代入這個專案自己
// 的 ToolContext 後的具體實例化,不是原生獨立宣告——ClientTool<Ctx> 是
// 「工具需不需要 context、context 長怎樣」這件事的通用表達(Ctx 預設 void,
// 對齊 SDK 原生 ToolHandler 完全不帶 context 的簽章),BridgeTool 只是把
// Ctx 固定代入 ToolContext,名字特意跟 sdk-proposals 的泛型型別區分開來
// (BridgeTool = 這個專案的 ClientToolsBridge 專屬、已具體化的版本;
// ClientTool<Ctx> = sdk-proposals 那邊給任意消費者用的通用提案型別)。這樣
// sdk-proposals 底下的檔案(defineTool.ts/toAgentBridgeTools.ts/
// arrayTools.ts 本身)完全不需要知道 ToolContext 長怎樣、甚至不需要 import
// 這個檔案,任何只想用 sdk-proposals、不碰 ClientToolsBridge.ts 的新元件都
// 能獨立成立——依賴方向是 BridgeTool 這邊引用 sdk-proposals 的通用型別再
// 具體化,不是反過來讓 sdk-proposals 向這裡借用型別。
export type BridgeTool = ClientTool<ToolContext>

// newLogId：不能用簡單的遞增計數器——原本在 React.StrictMode 下,開發模式會
// mount → cleanup → 再次 mount 同一個元件以偵測 effect 是否具備冪等性,若前
// 一輪 mount 的 WS 連線收尾跟後一輪有時間重疊,兩輪各自的計數器都從頭算起,
// 會各自生成 1、2、3...,同一個 log 清單裡出現重複的小整數 id,觸發 React 的
// duplicate key 警告(且理論上可能讓 reconciliation 對錯 DOM 節點)。這個顧慮
// 雖然是 React 特有的成因,但 bridge 拆成不含 React 依賴的 class 後仍保留同一
// 個 id 產生方式,以防未來有多個 bridge 實例並存或重建的情境。用時間戳記+亂數
// 尾碼,不管重建幾次都不會撞號。
function newLogId(): string {
  return Date.now().toString(36) + '_' + Math.random().toString(36).slice(2, 8)
}

// wsURL/testPromptURL：改成函式(延遲到 connect()/sendTestPrompt() 真正呼叫
// 時才求值),而不是模組頂層 const——ChatScreen.tsx 現在會 import
// ClientToolsBridge,形成循環 import(App.tsx → ChatScreen.tsx →
// ClientToolsBridge.ts → App.tsx,為了讀 BASE_URL)。App.tsx 的
// `export const BASE_URL` 定義在它自己 import ChatScreen 那行之後,若
// ClientToolsBridge.ts 在模組頂層立刻讀取 BASE_URL,會在 App.tsx 模組主體
// 還沒執行到 BASE_URL 那行、這個 binding 還在 TDZ(temporal dead zone)時
// 就被讀取,拋出「Cannot access 'BASE_URL' before initialization」。實測
// 到這個錯誤,改成函式呼叫時才讀 BASE_URL,此時整個模組圖已經初始化完成,
// 循環 import 本身還在但不再是問題。
function wsURL(): string {
  return BASE_URL.replace(/^http/, 'ws') + '/internal/clienttools/ws'
}
function testPromptURL(): string {
  return BASE_URL + '/internal/clienttools/test-prompt'
}

export type ConnStatus = 'connecting' | 'open' | 'closed'

export type LogEntry = {
  id: string
  dir: 'out' | 'in'
  summary: string
  detail?: string
}

// ClientToolsBridge 的建構子 callback 介面——每個欄位對應一種狀態變化,呼叫端
// (例如 ClientToolsDemo.tsx)把每個 callback 接到自己的 setState,讓 class
// 內部狀態變化時能通知外部,同時 class 本身完全不知道外部是不是 React。
export type ClientToolsBridgeCallbacks = {
  onStatusChange: (status: ConnStatus) => void
  onToolNamesChange: (toolNames: string[]) => void
  // onBatchQueried(選用):純讀取類工具(目前只有 trip_entry_list)透過
  // ToolContext.notifyBatchQueried 主動回報「剛查詢了這個 key」時觸發(見上方
  // ToolContext 型別定義處對這個機制的完整說明)——呼叫端(ChatScreen.tsx)
  // 需要的是「使用者這輪問了哪個清單」,不是「哪個清單的內容變了」,故用獨立
  // 的通知口子,不跟 setAllBatches 的寫入路徑混在一起。選用(帶 `?`)是因為
  // 既有呼叫端(ClientToolsDemo.tsx)不需要這個通知,比照 onSessionId 的既有
  // 慣例,新增選用欄位不影響既有呼叫端。
  onBatchQueried?: (key: string) => void
  onAssistantText: (text: string) => void
  onLog: (log: LogEntry[]) => void
  onBusyChange: (busy: boolean) => void
  // onSessionId(選用):ack 收到時回報這條連線的 sessionId(見下方 connect()
  // 的 ack 處理)。既有呼叫端(ClientToolsDemo.tsx)原本只把 sessionId 印進
  // debug log,不需要另外拿到它;新增這個獨立的可選 callback 純粹是新增,
  // 不改動既有的 ack 處理邏輯或任何既有欄位的行為——加這個口子是因為
  // ChatScreen.tsx 需要把 sessionId 一併帶進 POST /assist,讓後端的
  // trip_entry_* 工具能透過它把呼叫轉發回這條 WS 連線(見
  // server/internal/clienttools/interaction.go 的 InteractionAsker)。
  //
  // 收到 ack 時帶實際 sessionId 呼叫一次;連線關閉時(close 事件,見 connect()
  // 的 close handler)帶 null 呼叫一次——這條 WS 連線斷線後,後端在
  // clienttools/interaction.go 的 UnregisterAsker 會立刻讓這個 sessionID
  // 失效,若呼叫端(ChatScreen.tsx)沒有跟著清空自己存的值,之後送出的
  // assist 請求會帶著一個「看起來有值、實際上後端已經找不到人」的過期
  // sessionID,导致 trip_entry_* 工具呼叫失敗且錯誤訊息(no connected page
  // for session)完全看不出真正原因是「連線曾經斷過」。這個 bug 在實測時
  // 真的發生過,故 close 事件必須也回報一次讓呼叫端清空。
  onSessionId?: (sessionId: string | null) => void
}

export class ClientToolsBridge {
  private callbacks: ClientToolsBridgeCallbacks

  private status: ConnStatus = 'connecting'
  private log: LogEntry[] = []

  private ws: WebSocket | null = null
  private closedByDisconnect = false

  private ctx: ToolContext
  private handlers: Record<string, ClientToolHandler<ToolContext>>

  // constructor 收一個 BridgeTool 陣列(見上面型別定義),由呼叫端(例如
  // ClientToolsDemo.tsx)決定要餵給這個 bridge 哪些工具——bridge 本身不再
  // dependent 認得任何具體工具,新增一個工具不需要碰這個檔案,只要新建工具檔案
  // 並加進呼叫端的陣列。這裡把 tools 陣列轉成用 name 當 key 的 Record,存進
  // this.handlers 給 connect() 裡的 WS message handler 查表用。
  //
  // 「陣列轉 Record + 查重複就 throw」這段邏輯直接呼叫 sdk-proposals/
  // arrayTools.ts 的 toToolRecord,不再自己手寫一次同樣的迴圈——那本來就是
  // toToolRecord 存在的目的(見該函式的說明),這裡手寫一份是跟 sdk-proposals
  // 重複的邏輯,故直接複用。差異只在於 ctx 的綁定時機:toToolRecord 產出的
  // handler 維持兩個參數的形狀(args, ctx) => unknown(不像先前手動 curry
  // 成一個參數的寫法),ctx 改存成 this.ctx 這個私有欄位,呼叫時機延後到
  // connect() 的 WS message handler 真正查表執行時才一併傳入(見下方
  // handler(payload.args ?? {}, this.ctx))。
  //
  // getAllBatches/setAllBatches:bridge 不再自己持有 allBatches 的私有副本
  // (先前的設計是 bridge 內部存一份、透過 onEntriesChange 回呼通知外部——
  // 這造成 ChatScreen.tsx 的 clientToolsBatches state 與 bridge 內部副本
  // 是兩份獨立資料,任何繞過 bridge 直接改 state 的路徑(load() 讀裝置端 DB
  // 還原、entries_loaded WS 事件、TripListTable 刪除按鈕)都不會同步進 bridge,
  // 造成 trip_entry_list 等查詢工具讀到過期資料的 bug)。改成呼叫端直接注入
  // 讀寫函式,讓 ChatScreen.tsx 的 clientToolsBatchesRef 成為唯一真相來源——
  // bridge 每次工具執行都即時呼叫 getAllBatches() 讀當下最新值、setAllBatches()
  // 寫回去,不再自己快取。setAllBatches 內部呼叫端負責同時更新 ref 與觸發
  // React re-render(見 ChatScreen.tsx 建立 bridge 時傳入的實作)。
  constructor(
    tools: BridgeTool[],
    callbacks: ClientToolsBridgeCallbacks,
    getAllBatches: () => TripBatches,
    setAllBatches: (next: TripBatches) => void,
  ) {
    this.callbacks = callbacks

    this.ctx = {
      getAllBatches,
      setAllBatches,
      notifyBatchQueried: (key) => this.callbacks.onBatchQueried?.(key),
    }

    this.handlers = toToolRecord(tools)
  }

  private setStatus(next: ConnStatus) {
    this.status = next
    this.callbacks.onStatusChange(next)
  }

  private setBusy(next: boolean) {
    this.callbacks.onBusyChange(next)
  }

  private pushLog(dir: 'out' | 'in', summary: string, detail?: string) {
    this.log = [{ id: newLogId(), dir, summary, detail }, ...this.log].slice(0, 200)
    this.callbacks.onLog(this.log)
    // 收到後端傳來的訊息('in')後,若原本在等待,就不用等滿 90 秒——拿到任何
    // 回應都代表這一輪至少有進展;真正嚴謹的作法是追蹤 requestId 對應的完成事件,
    // 但這是試做,能看到「清單真的變了」比精確的 loading 狀態更重要。
    // 必須限定 dir === 'in':自己送出去的 log('out',例如 sendPrompt 裡
    // setBusy(true) 之後緊接著的 pushLog('out', 'prompt', ...))不代表後端有任何
    // 進展,不能當作解除等待的訊號——原本不分方向的寫法會讓 busy 在送出當下就被
    // 同步解除,只存在幾微秒,「推論中…」幾乎不會顯示、disabled 也擋不住重複送出。
    // 這是原本 React 版(watch log.length 的 useEffect)就存在、重構時如實保留
    // 下來的既有缺陷,在此修正。
    if (dir === 'in') this.setBusy(false)
  }

  private send(env: Envelope) {
    this.ws?.send(JSON.stringify(env))
  }

  // connect：建立 WS 連線,對應原本 useEffect 裡的邏輯(open/close/error/
  // message 四個事件監聽 + hello 交握)。
  connect() {
    this.closedByDisconnect = false
    const ws = new WebSocket(wsURL())
    this.ws = ws

    ws.addEventListener('open', () => {
      this.pushLog('out', 'hello', JSON.stringify({ appId: 'clienttools' }))
      this.send({ type: 'hello', payload: { appId: 'clienttools' } })
    })

    ws.addEventListener('close', () => {
      if (!this.closedByDisconnect) this.setStatus('closed')
      // 連線一斷,後端就會 UnregisterAsker 讓這個 sessionID 失效(見上方
      // onSessionId 型別定義的說明)——不論是 disconnect() 主動關閉還是意外
      // 斷線(closedByDisconnect 是哪個值都一樣),呼叫端存的 sessionId 都
      // 必須跟著清空,避免之後用一個已經死掉的 sessionID 發起 assist 請求。
      this.callbacks.onSessionId?.(null)
    })

    ws.addEventListener('error', () => {
      // close 事件會隨後觸發,狀態更新交給它處理,這裡不需要額外動作
      // (同 agent client.ts 的 AgentBridge 的作法)。
    })

    ws.addEventListener('message', (ev) => {
      let env: Envelope
      try {
        env = JSON.parse(String(ev.data))
      } catch {
        return
      }

      switch (env.type) {
        case 'ack': {
          const ack = env.payload as AckPayload
          this.setStatus('open')
          this.callbacks.onToolNamesChange(ack.toolNames)
          this.callbacks.onSessionId?.(ack.sessionId)
          this.pushLog('in', `ack (session ${ack.sessionId})`, ack.toolNames.join(', '))

          // 連線時檢查前後端工具清單是否一致:後端的 ack.toolNames 來自
          // server/tools/clienttools.yaml,前端的 handlers 來自建構子傳入的
          // tools 陣列,兩邊只靠名稱字串對齊。若 YAML 加了工具但前端忘了註冊
          // (或拼錯字),不檢查的話要等到 LLM 真的呼叫時才收到 no handler
          // registered——那時已浪費一輪真實推論。在 ack 當下抓出設定飄移,
          // 不要等到推論中途才爆。
          const frontendNames = Object.keys(this.handlers)
          // 後端有、前端沒有:LLM 可能呼叫一個前端沒實作的工具,較危險的方向。
          const backendOnly = ack.toolNames.filter((n) => !frontendNames.includes(n))
          // 前端有、後端沒有:前端註冊了 LLM 根本不知道的工具,浪費但不會壞。
          const frontendOnly = frontendNames.filter((n) => !ack.toolNames.includes(n))
          if (backendOnly.length > 0) {
            this.pushLog(
              'in',
              '⚠ 工具清單不一致:後端有、前端未註冊',
              `後端 clienttools.yaml 宣告了但前端沒有對應 handler 的工具:${backendOnly.join(', ')}——LLM 若呼叫這些工具會收到 no handler registered`,
            )
          }
          if (frontendOnly.length > 0) {
            this.pushLog(
              'in',
              '⚠ 工具清單不一致:前端有、後端未宣告',
              `前端註冊了但後端 clienttools.yaml 沒有宣告的工具:${frontendOnly.join(', ')}——LLM 不知道這些工具存在,永遠不會被呼叫`,
            )
          }
          break
        }
        case 'tool_call':
        case 'tool_query': {
          // 兩者在前端這側走完全相同的處理:查 handler 表、執行、回 tool_result。
          // 後端要不要等這個結果,是 server 那側的差異(見 clienttools_ws.go),
          // 前端不需要也不應該知道自己收到的是哪一種——這正是 agent 專案
          // client.ts 的 handleToolCall 設計成兩個 case 共用同一段程式碼的原因。
          const payload = env.payload as ToolCallPayload
          const handler = this.handlers[payload.toolName]
          this.pushLog('in', `${env.type}: ${payload.toolName}`, JSON.stringify(payload.args))

          if (!handler) {
            this.pushLog('out', `tool_result: ${payload.toolName} (no handler)`)
            this.send({
              type: 'tool_result',
              requestId: env.requestId,
              payload: { toolName: payload.toolName, ok: false, error: `no handler registered for tool "${payload.toolName}"` },
            })
            break
          }

          try {
            const result = handler(payload.args ?? {}, this.ctx)
            this.pushLog('out', `tool_result: ${payload.toolName} ok`, JSON.stringify(result))
            this.send({
              type: 'tool_result',
              requestId: env.requestId,
              payload: { toolName: payload.toolName, ok: true, result: result ?? null },
            })
          } catch (err) {
            const msg = err instanceof Error ? err.message : String(err)
            this.pushLog('out', `tool_result: ${payload.toolName} error`, msg)
            this.send({
              type: 'tool_result',
              requestId: env.requestId,
              payload: { toolName: payload.toolName, ok: false, error: msg },
            })
          }
          break
        }
        case 'assistant_message': {
          const p = env.payload as AssistantMessagePayload
          this.callbacks.onAssistantText(p.text)
          this.pushLog('in', 'assistant_message', p.text)
          break
        }
        case 'error': {
          const p = env.payload as ErrorPayload
          this.pushLog('in', 'error', p.message)
          break
        }
      }
    })
  }

  // disconnect：對應原本 useEffect 的 cleanup,關閉連線。
  disconnect() {
    this.closedByDisconnect = true
    this.ws?.close()
    this.ws = null
  }

  // sendPrompt：走 WS 送出使用者輸入的一句話。回傳 boolean 表示「有沒有真的送
  // 出去」——呼叫端(ClientToolsDemo.tsx)用這個回傳值判斷是否該清空自己的
  // input state(原本 sendPrompt 是送出當下就 setInput('')）。
  sendPrompt(text: string): boolean {
    const trimmed = text.trim()
    if (!trimmed || this.status !== 'open' || !this.ws) return false
    this.setBusy(true)
    this.callbacks.onAssistantText('')
    this.pushLog('out', 'prompt', trimmed)
    this.send({ type: 'prompt', requestId: 'p_' + Date.now(), payload: { text: trimmed } })
    // 沒有明確的「這輪推論結束」訊號可等(assistant_message 是可選的——純工具呼叫的
    // 回合可能完全不送文字),用短暫的 busy 提示 + 觀察下面清單/log 變化即可。
    // 90 秒是後端 ClientToolsAnalyzer.Prompt 的逾時上限(clienttools_agent.go),
    // 這裡用同樣長度的保底計時器解除 busy,避免真的卡住時按鈕永遠disabled。
    window.setTimeout(() => this.setBusy(false), 90_000)
    return true
  }

  // sendTestPrompt：走 POST /internal/clienttools/test-prompt,不需要自己開
  // WS 連線也能觸發同一個 session 的推論。回傳的 Promise 在 fetch 的
  // finally 之後才 resolve,呼叫端可用這個時機知道何時該清空 input(原本
  // sendTestPrompt 是 fetch 的 finally 才 setInput('')，跟 sendPrompt 送出
  // 當下就清空的時機不同,搬到 class 後用「await 回傳的 Promise 再清空」保留
  // 同樣的行為差異)。
  async sendTestPrompt(text: string): Promise<void> {
    const trimmed = text.trim()
    if (!trimmed) return
    this.setBusy(true)
    this.callbacks.onAssistantText('')
    this.pushLog('out', 'POST /internal/clienttools/test-prompt', trimmed)
    try {
      const res = await fetch(testPromptURL(), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: trimmed }),
      })
      const data = await res.json()
      if (!res.ok) {
        this.pushLog('in', 'test-prompt error', JSON.stringify(data))
      } else {
        this.pushLog('in', 'test-prompt reply', JSON.stringify(data))
        if (data.reply) this.callbacks.onAssistantText(data.reply)
      }
    } catch (err) {
      this.pushLog('in', 'test-prompt fetch failed', err instanceof Error ? err.message : String(err))
    } finally {
      this.setBusy(false)
    }
  }
}
