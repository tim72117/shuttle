import { useEffect, useRef, useState } from 'react'
import { AgentBridge } from '@onagent/bridge'
import { isSubmitEnter } from '../App'
import { tripEntryAdd, newTripEntryId } from './tools/tripEntryAdd'
import { tripEntryList } from './tools/tripEntryList'
import { tripListBatches } from './tools/tripListBatches'
import { tripEntryDelete } from './tools/tripEntryDelete'
import { tripEntryUpdate } from './tools/tripEntryUpdate'
import type { TripBatches } from './tripEntryTools'
import { toAgentBridgeTools } from '../sdk-proposals/toAgentBridgeTools'

// OnagentToolCtx — 這個測試頁面需要的 context 形狀,結構對齊
// ClientToolsBridge.ts 的 ToolContext,但刻意在這裡自己宣告一份,不 import
// 那個檔案——這個頁面走的是完全獨立於 ClientToolsBridge 的資料流(見上方
// 說明:透過 onagent 平台,不是 tripace 自己的 clienttools_ws.go),不該為了
// 借一個型別而牽連進 ClientToolsBridge.ts 的 import 圖。
//
// 這裡需要完整三個欄位(getAllBatches/setAllBatches/notifyBatchQueried),
// 跟個別工具檔案(tripEntryAdd.ts 等)各自宣告最小子集不同——因為
// toAgentBridgeTools 的簽章是 toAgentBridgeTools<Ctx>(tools: ClientTool<Ctx>[],
// ctx: Ctx, ...),一次陣列只能有一個 Ctx,而這裡混了五個工具、各自最小需求
// 不同(tripEntryAdd/Delete/Update 要 setAllBatches,tripEntryList 要
// notifyBatchQueried,tripListBatches 只要 getAllBatches),ctx 必須同時滿足
// 全部五個工具的需求聯集,也就是完整的三個欄位,沒有更小的子集可用。
type OnagentToolCtx = {
  getAllBatches: () => TripBatches
  setAllBatches: (next: TripBatches) => void
  notifyBatchQueried: (key: string) => void
}

// SEED_BATCH_KEY:預先放一批種子資料,讓 trip_entry_list 一開始就有東西可
// 查(不用先靠 trip_entry_add 造資料才能測查詢),比照 ClientToolsDemo.tsx
// 的既有慣例(同樣用固定 key 放示範資料)。
const SEED_BATCH_KEY = 'demo'

// OnagentBridgeDemo — 測試用:透過 onagent 平台(而非這個專案自己的
// ClientToolsBridge/clienttools_ws.go)呼叫既有的 trip_entry_add/
// trip_entry_list/trip_list_batches/trip_entry_delete/trip_entry_update
// 這五個 ClientTool。不接正式資料流(allBatches 只存在這個元件的 state,
// 重新整理頁面就消失,不進裝置端 DB)。
//
// trip_entry_delete 在 tripace 正式系統(server/internal/llm/assistant_agent.go
// 的白名單)刻意不開放給 LLM 用(避免 LLM 直接刪除使用者資料),但這個測試
// 頁面不受該白名單限制,五個工具都推送、都接了 handler——純測試環境,
// 不代表正式系統也開放刪除。trip_entry_delete/trip_entry_update 在
// server/tools/onagent-tools.yaml 標的是 kind: action(跟 tripace 自家
// clienttools.yaml 一致),回傳值只用來判斷成功/失敗,不會被 onagent 後端
// 塞進 LLM 的推論 context。
//
// onagent 的 AgentBridge 協定(hello/ack/tool_call/tool_result/prompt/
// assistant_message/error)跟這個專案自己的 ClientToolsBridge 幾乎同構,
// 差別在於 tools map 的 handler 簽章更單純(只收 args、回傳 result),ClientTool
// 則多收一個 ToolContext(getAllBatches/setAllBatches/notifyBatchQueried)。
// 這裡直接重用這五個 ClientTool 物件本身(而非只借用它們內部的純函式邏輯),
// 用 toAgentBridgeTools(見 sdk-proposals/ 目錄的說明——模擬「若 SDK 願意
// 補上陣列註冊功能」的試做)把整批工具一次轉成 AgentBridge 要的 tools map,
// 不用像最初那版手動為每個工具寫一行轉接程式碼,新增工具只需要加進下面的
// 陣列——**但同時要記得 onagent 平台上這個 app 也要推送對應的 tool schema
// (用 onagent save-tools),兩邊要保持同步:平台有 schema、前端沒 handler
// 會讓 LLM 呼叫時收到 no handler registered;前端有 handler、平台沒 schema
// 則 LLM 永遠不知道這個工具存在、永遠不會呼叫到(這正是 trip_list_batches
// 一度推送了 schema、前端卻忘了加 handler 的實際案例)**。
//
// VITE_ONAGENT_APP_KEY 未設時整個元件顯示提示,不嘗試連線(apiKey 是必要的
// —— 沒有它 WS 交握不會被後端接受)。VITE_ONAGENT_URL 是 onagent 平台自己
// 的位址,跟 VITE_API_BASE(tripace-server,8080)是完全獨立的兩個後端——
// 本機 onagent 服務監聽 8081,不能借用 VITE_API_BASE 推導,正式環境
// onagent 平台幾乎必然部署在不同 host。fallback 預設值也對齊本機的 8081
// (先前曾誤用 8080,見 .env.development 的說明)。
const APP_ID = 'tripace'
const WS_URL = (import.meta.env.VITE_ONAGENT_URL ?? 'http://localhost:8081').replace(/^http/, 'ws') + '/ws'

type LogEntry = { id: string; text: string }

export function OnagentBridgeDemo() {
  const apiKey = import.meta.env.VITE_ONAGENT_APP_KEY as string | undefined
  const [status, setStatus] = useState<'connecting' | 'ready' | 'closed'>('connecting')
  const [log, setLog] = useState<LogEntry[]>([])
  const [prompt, setPrompt] = useState('')
  // 初始值放兩筆種子資料在 SEED_BATCH_KEY 底下,讓 trip_entry_list 一開始
  // 就有東西可查(直接問「demo 這批清單裡有什麼」就能測查詢,不用先靠
  // trip_entry_add 造資料)。
  const [allBatches, setAllBatches] = useState<TripBatches>({
    [SEED_BATCH_KEY]: [
      { id: newTripEntryId(), title: '東京晴空塔', date: '2026-07-19', time: '10:00', note: '先上樓看夜景' },
      { id: newTripEntryId(), title: '築地場外市場早餐', date: '2026-07-20', time: '08:00', note: '' },
    ],
  })
  const allBatchesRef = useRef<TripBatches>(allBatches)
  useEffect(() => {
    allBatchesRef.current = allBatches
  }, [allBatches])
  const bridgeRef = useRef<AgentBridge | null>(null)
  // lastTouchedKey:最近一次被工具改動或查詢過的 key,用來在下方表格區
  // 高亮對應的批次,讓「工具剛剛做了什麼」不用逐行讀 log 也能一眼看出來。
  const [lastTouchedKey, setLastTouchedKey] = useState<string | null>(null)

  const pushLog = (text: string) => setLog((prev) => [{ id: crypto.randomUUID(), text }, ...prev].slice(0, 50))

  useEffect(() => {
    if (!apiKey) return
    // onagentToolContext:讓 tripEntryAdd/tripEntryList(ClientTool)在 onagent
    // 的連線底下也能執行的 adapter——getAllBatches/setAllBatches 讀寫
    // allBatchesRef;notifyBatchQueried 由 tripEntryList 呼叫(純讀取工具,
    // 用它回報「剛查詢了這個 key」,見 tripEntryList.ts 的說明),這裡接上
    // log 讓查詢確實觸發時看得到。
    const onagentToolContext: OnagentToolCtx = {
      getAllBatches: () => allBatchesRef.current,
      setAllBatches: (next) => {
        allBatchesRef.current = next
        setAllBatches(next)
      },
      notifyBatchQueried: (key) => {
        pushLog(`trip_entry_list 查詢通知: key=${key}`)
        setLastTouchedKey(key)
      },
    }
    const bridge = new AgentBridge({
      url: WS_URL,
      appId: APP_ID,
      apiKey,
      // toAgentBridgeTools:整批轉換,取代原本手動為 trip_entry_add/
      // trip_entry_list 各寫一行轉接程式碼的版本(見 sdk-proposals/
      // toAgentBridgeTools.ts 的完整說明)。新增工具只需要加進下面的陣列,
      // 不用再多寫一行轉接。onToolResult 補回原本手動列舉時「每個工具各自
      // pushLog 一行」的可觀測性。
      tools: toAgentBridgeTools(
        [tripEntryAdd, tripEntryList, tripListBatches, tripEntryDelete, tripEntryUpdate],
        onagentToolContext,
        ({ name, result }) => {
          pushLog(`${name} 執行成功: ${JSON.stringify(result)}`)
          // trip_entry_add 的 result 帶 key(見 tripEntryAdd.ts 的 addTripEntry
          // 回傳形狀),用它標記表格高亮——trip_entry_list 的 result 沒有 key
          // (只有 entries/total),它的高亮已經由上面的 notifyBatchQueried 處理;
          // trip_entry_delete/trip_entry_update 是 action 型(見
          // server/tools/onagent-tools.yaml),onagent 後端根本不會把 result
          // 餵回 LLM,這個 onToolResult callback 仍會被呼叫(它是前端自己攔截
          // 用來畫面顯示,不受 kind 影響),但兩者的 result 形狀({deleted:id}/
          // {updated:id})都沒有 key 欄位,不會誤觸這裡的高亮邏輯。用 in 操作子
          // 安全地判斷 result 是否帶 key,不假設所有工具的 result 形狀一致。
          if (result && typeof result === 'object' && 'key' in result && typeof result.key === 'string') {
            setLastTouchedKey(result.key)
          }
        },
      ),
      onAssistantMessage: (text) => pushLog(`assistant: ${text}`),
      onError: (err) => pushLog(`error: ${err.message}`),
    })
    bridgeRef.current = bridge
    setStatus('connecting')
    // AgentBridge 沒有連線成功的 callback(ack 只在內部處理),用送出後短暫
    // 延遲樂觀顯示 ready——純測試用途,不追求精確的連線狀態機。
    const t = window.setTimeout(() => setStatus('ready'), 500)
    return () => {
      window.clearTimeout(t)
      bridge.close()
      bridgeRef.current = null
      setStatus('closed')
    }
  }, [apiKey])

  const sendPrompt = () => {
    const text = prompt.trim()
    if (!text || !bridgeRef.current) return
    pushLog(`送出 prompt: ${text}`)
    bridgeRef.current.prompt(text)
    setPrompt('')
  }

  if (!apiKey) {
    return (
      <div style={{ padding: 24 }}>
        <p>未設定 VITE_ONAGENT_APP_KEY,請先在 web/.env.development.local 加入 tripace app 的 apiKey。</p>
      </div>
    )
  }

  return (
    <>
      <div className="navbar">
        <span style={{ width: 36 }} />
        <span className="title">onagent 串接試做(trip_entry_add/list/list_batches/delete/update)</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body" style={{ padding: 16 }}>
        <div style={{ marginBottom: 12, opacity: 0.7 }}>連線狀態: {status}</div>
        <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
          <input
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => isSubmitEnter(e) && sendPrompt()}
            placeholder="輸入一句話,例如「幫我加一筆晚餐吃火鍋」"
            style={{ flex: 1, padding: 8 }}
          />
          <button className="btn-secondary" onClick={sendPrompt} disabled={status !== 'ready'}>
            送出
          </button>
        </div>
        <div style={{ marginBottom: 16 }}>
          <strong>目前 allBatches(僅此元件記憶體,不持久化)——每張表是一個批次(key),
          最近被 trip_entry_add/trip_entry_list 動到的那批會highlight:</strong>
          {Object.keys(allBatches).length === 0 ? (
            <div style={{ opacity: 0.6, marginTop: 8 }}>目前沒有任何批次。</div>
          ) : (
            Object.entries(allBatches).map(([key, entries]) => (
              <div
                key={key}
                style={{
                  margin: '8px 0', padding: '10px 12px', borderRadius: 10,
                  border: key === lastTouchedKey
                    ? '2px solid var(--accent, #2d7ff9)'
                    : '1px solid var(--border, #33333322)',
                  background: 'var(--surface, #00000008)', fontSize: 13,
                }}
              >
                <div style={{ opacity: 0.6, marginBottom: 6, fontSize: 12 }}>
                  批次:{key}{key === lastTouchedKey && '(剛被工具動過)'}
                </div>
                {entries.length === 0 ? (
                  <div style={{ opacity: 0.6 }}>這批目前是空的。</div>
                ) : (
                  <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                    <thead>
                      <tr style={{ textAlign: 'left', opacity: 0.6 }}>
                        <th style={{ fontWeight: 400, paddingRight: 8 }}>標題</th>
                        <th style={{ fontWeight: 400, paddingRight: 8 }}>日期</th>
                        <th style={{ fontWeight: 400, paddingRight: 8 }}>時刻</th>
                        <th style={{ fontWeight: 400 }}>備註</th>
                      </tr>
                    </thead>
                    <tbody>
                      {entries.map((e) => (
                        <tr key={e.id}>
                          <td style={{ paddingRight: 8 }}>{e.title}</td>
                          <td style={{ paddingRight: 8 }}>{e.date}</td>
                          <td style={{ paddingRight: 8 }}>{e.time}</td>
                          <td>{e.note}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            ))
          )}
        </div>
        <div>
          <strong>Log:</strong>
          {log.map((l) => (
            <div key={l.id} style={{ fontSize: 12, padding: '4px 0', borderBottom: '1px solid var(--border, #33333322)' }}>
              {l.text}
            </div>
          ))}
        </div>
      </div>
    </>
  )
}
