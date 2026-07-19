import { useCallback, useEffect, useRef, useState } from 'react'
import { BASE_URL } from './App'

// ClientToolsDemo — 「LLM 呼叫前端 tool」試做(POC)。
//
// 一份旅程 entry 清單只存在這個元件的 React state(不進任何後端資料庫,重新整理
// 頁面就會消失)。使用者打一句話送給後端,後端 want agent(server/internal/llm
// 的獨立 clienttools agent role,見 clienttools_agent.go)決定要呼叫
// trip_entry_add / trip_entry_delete / trip_entry_update / trip_entry_list
// 中的哪一個,透過 WebSocket 把呼叫轉發回這個頁面;下面的 bridge 收到後執行對應
// 的前端函式(直接改這個元件的 state),再把結果送回去。
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

type TripEntry = {
  id: string
  title: string
  date: string
  time: string
  note: string
}

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

// ---- 前端工具 handler:直接操作 React state,不呼叫任何後端 API ----
//
// 回傳值的形狀對齊 server/tools/clienttools.yaml 各工具的 returns 欄位定義:
// trip_entry_add/trip_entry_list 是 query 型,回傳值會真的被送回 LLM 的推論
// context;trip_entry_delete/trip_entry_update 是 action 型,回傳值只用來判斷
// 「有沒有成功」,LLM 看不到實際內容。
type ToolHandler = (args: Record<string, unknown>) => unknown

function newEntryId(): string {
  return 'trip_' + Math.random().toString(36).slice(2, 10)
}

// newLogId：不能用簡單的遞增計數器(如 useRef(0)+=1)——React.StrictMode 在開發
// 模式下會 mount → cleanup → 再次 mount 同一個元件以偵測 effect 是否具備冪等性,
// 若前一輪 mount 的 WS 連線收尾跟後一輪有時間重疊,兩輪各自的 useRef 計數器都從
// 頭算起,會各自生成 1、2、3...,同一個 log 清單裡出現重複的小整數 id,
// 觸發 React 的 duplicate key 警告(且理論上可能讓 reconciliation 對錯 DOM 節點)。
// 用時間戳記+亂數尾碼,不管重掛載幾次都不會撞號。
function newLogId(): string {
  return Date.now().toString(36) + '_' + Math.random().toString(36).slice(2, 8)
}

function asString(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

const WS_URL = BASE_URL.replace(/^http/, 'ws') + '/internal/clienttools/ws'
const TEST_PROMPT_URL = BASE_URL + '/internal/clienttools/test-prompt'

type ConnStatus = 'connecting' | 'open' | 'closed'

type LogEntry = {
  id: string
  dir: 'out' | 'in'
  summary: string
  detail?: string
}

export function ClientToolsDemo() {
  const [entries, setEntries] = useState<TripEntry[]>([
    { id: newEntryId(), title: '東京晴空塔', date: '2026-07-19', time: '10:00', note: '先上樓看夜景' },
    { id: newEntryId(), title: '築地場外市場早餐', date: '2026-07-20', time: '08:00', note: '' },
  ])
  const entriesRef = useRef(entries)
  entriesRef.current = entries

  const [status, setStatus] = useState<ConnStatus>('connecting')
  const [toolNames, setToolNames] = useState<string[]>([])
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [assistantText, setAssistantText] = useState('')
  const [log, setLog] = useState<LogEntry[]>([])

  const wsRef = useRef<WebSocket | null>(null)

  const pushLog = useCallback((dir: 'out' | 'in', summary: string, detail?: string) => {
    setLog((prev) => [{ id: newLogId(), dir, summary, detail }, ...prev].slice(0, 200))
  }, [])

  // ---- 前端工具實作:與 server/tools/clienttools.yaml 宣告的四個工具一一對應 ----
  // useRef 存 handler 表(而非閉包捕捉 state):bridge 的訊息處理函式建立一次、
  // 長期存在,handler 內用 setEntries(updater) 的 functional 形式讀寫最新 state,
  // 不需要每次 entries 變動就重建 WebSocket 訊息處理邏輯。
  const handlersRef = useRef<Record<string, ToolHandler>>({})
  handlersRef.current = {
    trip_entry_add: (args) => {
      const entry: TripEntry = {
        id: newEntryId(),
        title: asString(args.title) || '(未命名項目)',
        date: asString(args.date),
        time: asString(args.time),
        note: asString(args.note),
      }
      setEntries((prev) => [...prev, entry])
      return { id: entry.id, title: entry.title, date: entry.date }
    },
    // trip_entry_delete/trip_entry_update 的「找不找得到這筆」判斷,一律先讀
    // entriesRef.current(每次渲染都同步更新,見上面的 entriesRef.current =
    // entries)決定,再呼叫 setEntries——不要用「setEntries 的 updater 內部
    // 設一個外層 let 變數」這種側寫法。後者在一次使用者輸入觸發同一輪推論裡
    // 連續呼叫多個工具時(如先 trip_entry_add 再緊接著 trip_entry_update)
    // 實測會出現「setEntries 真的套用了更新、但外層的 found 變數卻讀到
    // updater 尚未認定找到」的競態,導致 tool_result 回報 error,即使畫面上
    // 那筆其實已經改成功——回給 LLM 的結果因此跟畫面實際狀態不一致,LLM 會
    // 誤以為操作失敗並如實(但錯誤地)告知使用者。改成「先用當下已確定穩定
    // 的 entriesRef.current 判斷存在與否,存在才呼叫 setEntries」,兩者判斷
    // 依據同一份快照,不會再分岔。
    trip_entry_delete: (args) => {
      const id = asString(args.id)
      if (!entriesRef.current.some((e) => e.id === id)) {
        throw new Error(`entry ${id} not found`)
      }
      setEntries((prev) => prev.filter((e) => e.id !== id))
      return { deleted: id }
    },
    trip_entry_update: (args) => {
      const id = asString(args.id)
      if (!entriesRef.current.some((e) => e.id === id)) {
        throw new Error(`entry ${id} not found`)
      }
      setEntries((prev) =>
        prev.map((e) => {
          if (e.id !== id) return e
          return {
            ...e,
            title: asString(args.title) || e.title,
            date: asString(args.date) || e.date,
            time: args.time !== undefined && asString(args.time) !== '' ? asString(args.time) : e.time,
            note: args.note !== undefined && asString(args.note) !== '' ? asString(args.note) : e.note,
          }
        }),
      )
      return { updated: id }
    },
    trip_entry_list: () => {
      return { entries: entriesRef.current }
    },
  }

  useEffect(() => {
    let closedByEffect = false
    const ws = new WebSocket(WS_URL)
    wsRef.current = ws

    const send = (env: Envelope) => {
      ws.send(JSON.stringify(env))
    }

    ws.addEventListener('open', () => {
      pushLog('out', 'hello', JSON.stringify({ appId: 'clienttools' }))
      send({ type: 'hello', payload: { appId: 'clienttools' } })
    })

    ws.addEventListener('close', () => {
      if (!closedByEffect) setStatus('closed')
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
          setStatus('open')
          setToolNames(ack.toolNames)
          pushLog('in', `ack (session ${ack.sessionId})`, ack.toolNames.join(', '))
          break
        }
        case 'tool_call':
        case 'tool_query': {
          // 兩者在前端這側走完全相同的處理:查 handler 表、執行、回 tool_result。
          // 後端要不要等這個結果,是 server 那側的差異(見 clienttools_ws.go),
          // 前端不需要也不應該知道自己收到的是哪一種——這正是 agent 專案
          // client.ts 的 handleToolCall 設計成兩個 case 共用同一段程式碼的原因。
          const payload = env.payload as ToolCallPayload
          const handler = handlersRef.current[payload.toolName]
          pushLog('in', `${env.type}: ${payload.toolName}`, JSON.stringify(payload.args))

          if (!handler) {
            pushLog('out', `tool_result: ${payload.toolName} (no handler)`)
            send({
              type: 'tool_result',
              requestId: env.requestId,
              payload: { toolName: payload.toolName, ok: false, error: `no handler registered for tool "${payload.toolName}"` },
            })
            break
          }

          try {
            const result = handler(payload.args ?? {})
            pushLog('out', `tool_result: ${payload.toolName} ok`, JSON.stringify(result))
            send({
              type: 'tool_result',
              requestId: env.requestId,
              payload: { toolName: payload.toolName, ok: true, result: result ?? null },
            })
          } catch (err) {
            const msg = err instanceof Error ? err.message : String(err)
            pushLog('out', `tool_result: ${payload.toolName} error`, msg)
            send({
              type: 'tool_result',
              requestId: env.requestId,
              payload: { toolName: payload.toolName, ok: false, error: msg },
            })
          }
          break
        }
        case 'assistant_message': {
          const p = env.payload as AssistantMessagePayload
          setAssistantText(p.text)
          pushLog('in', 'assistant_message', p.text)
          break
        }
        case 'error': {
          const p = env.payload as ErrorPayload
          pushLog('in', 'error', p.message)
          break
        }
      }
    })

    return () => {
      closedByEffect = true
      ws.close()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在掛載時連線一次;handler 表透過 ref 存取最新版本。
  }, [pushLog])

  const sendPrompt = useCallback(() => {
    const text = input.trim()
    if (!text || status !== 'open' || !wsRef.current) return
    setBusy(true)
    setAssistantText('')
    pushLog('out', 'prompt', text)
    wsRef.current.send(JSON.stringify({ type: 'prompt', requestId: 'p_' + Date.now(), payload: { text } }))
    setInput('')
    // 沒有明確的「這輪推論結束」訊號可等(assistant_message 是可選的——純工具呼叫的
    // 回合可能完全不送文字),用短暫的 busy 提示 + 觀察下面清單/log 變化即可。
    // 90 秒是後端 ClientToolsAnalyzer.Prompt 的逾時上限(clienttools_agent.go),
    // 這裡用同樣長度的保底計時器解除 busy,避免真的卡住時按鈕永遠disabled。
    window.setTimeout(() => setBusy(false), 90_000)
  }, [input, status, pushLog])

  // 收到 assistant_message 或任何訊息後,若原本在等待,就不用等滿 90 秒——
  // 用簡單的訊號:log 一有新項目就解除 busy(WS 是雙向阻塞協定,拿到任何回應
  // 都代表這一輪至少有進展;真正嚴謹的作法是追蹤 requestId 對應的完成事件,
  // 但這是試做,能看到「清單真的變了」比精確的 loading 狀態更重要)。
  useEffect(() => {
    if (log.length > 0) setBusy(false)
  }, [log])

  const sendTestPrompt = useCallback(async () => {
    const text = input.trim()
    if (!text) return
    setBusy(true)
    setAssistantText('')
    pushLog('out', 'POST /internal/clienttools/test-prompt', text)
    try {
      const res = await fetch(TEST_PROMPT_URL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      })
      const data = await res.json()
      if (!res.ok) {
        pushLog('in', 'test-prompt error', JSON.stringify(data))
      } else {
        pushLog('in', 'test-prompt reply', JSON.stringify(data))
        if (data.reply) setAssistantText(data.reply)
      }
    } catch (err) {
      pushLog('in', 'test-prompt fetch failed', err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
      setInput('')
    }
  }, [input, pushLog])

  return (
    <div className="cts-root">
      <div className="cts-main">
        <div className="cts-header">
          <span className="cts-title">旅程清單(僅存在此頁面記憶體,不進資料庫)</span>
          <span className={`cts-status cts-status-${status}`}>
            {status === 'open' ? `已連線 · ${toolNames.length} 個工具` : status === 'connecting' ? '連線中…' : '已斷線'}
          </span>
        </div>

        <div className="cts-entries">
          {entries.length === 0 ? (
            <div className="cts-empty">目前清單是空的。</div>
          ) : (
            entries.map((e) => (
              <div key={e.id} className="cts-entry">
                <div className="cts-entry-main">
                  <span className="cts-entry-title">{e.title}</span>
                  <span className="cts-entry-when">
                    {e.date}
                    {e.time ? ` ${e.time}` : ''}
                  </span>
                </div>
                {e.note && <div className="cts-entry-note">{e.note}</div>}
                <div className="cts-entry-id">{e.id}</div>
              </div>
            ))
          )}
        </div>

        {assistantText && <div className="cts-assistant">{assistantText}</div>}

        <div className="cts-inputrow">
          <input
            className="cts-input"
            placeholder="跟 LLM 說一句話,例如「幫我新增一筆明天的東京晴空塔行程」"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !busy) sendPrompt()
            }}
            disabled={busy}
          />
          <button className="btn-secondary" onClick={sendPrompt} disabled={busy || status !== 'open' || !input.trim()}>
            {busy ? '推論中…' : '送出(WS）'}
          </button>
          <button
            className="btn-secondary"
            onClick={sendTestPrompt}
            disabled={busy || !input.trim()}
            title="走 POST /internal/clienttools/test-prompt,不需要自己開 WS 連線也能觸發同一個 session 的推論"
          >
            測試端點
          </button>
        </div>
      </div>

      <div className="cts-log">
        <div className="cts-log-title">WS / HTTP 訊息記錄</div>
        <div className="cts-log-list">
          {log.map((l) => (
            <div key={l.id} className={`cts-log-entry cts-log-${l.dir}`}>
              <div className="cts-log-summary">
                <span className="cts-log-dir">{l.dir === 'out' ? '→' : '←'}</span> {l.summary}
              </div>
              {l.detail && <div className="cts-log-detail">{l.detail}</div>}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
