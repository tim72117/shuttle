import { useCallback, useEffect, useState } from 'react'
import type { ApiCall, ClientConfig } from './api'
import * as api from './api'
import type { Channel, Entry } from './types'

// Debug panel:兩個分頁 —— API 交易紀錄、目前頻道的 Entry 條目。
// API:依時間倒序列出每筆交易,點開看原始 request/response JSON。
// Entries:看 record_entry 工具記了哪些結構化條目(item + 時間)。

type DebugTab = 'api' | 'entries'

export function DebugPanel({
  calls,
  onClear,
  cfg,
  channel,
}: {
  calls: ApiCall[]
  onClear: () => void
  cfg: ClientConfig
  channel: Channel | null
}) {
  const [tab, setTab] = useState<DebugTab>('api')

  return (
    <div className="debug">
      <div className="debug-head">
        <div className="debug-tabs">
          <button
            className={tab === 'api' ? 'active' : ''}
            onClick={() => setTab('api')}
          >
            ⚡ API · {calls.length}
          </button>
          <button
            className={tab === 'entries' ? 'active' : ''}
            onClick={() => setTab('entries')}
          >
            📅 Entries
          </button>
        </div>
        {tab === 'api' && <button onClick={onClear}>清除</button>}
      </div>
      {tab === 'api' ? (
        <div className="debug-list">
          {calls.length === 0 ? (
            <div style={{ color: '#6e6e78', padding: 16, textAlign: 'center' }}>
              尚無請求。在左側操作 app,這裡會即時記錄每次 API 交易。
            </div>
          ) : (
            calls.map((c) => <CallRow key={c.id} call={c} />)
          )}
        </div>
      ) : (
        <EntriesView cfg={cfg} channel={channel} />
      )}
    </div>
  )
}

// EntriesView 顯示目前頻道的 Entry 條目(record_entry 工具寫進 DB 的結構化資料)。
function EntriesView({
  cfg,
  channel,
}: {
  cfg: ClientConfig
  channel: Channel | null
}) {
  const [entries, setEntries] = useState<Entry[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    if (!channel) return
    setLoading(true)
    setErr(null)
    try {
      setEntries(await api.fetchEntries(cfg, channel.id))
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel?.id])

  useEffect(() => {
    load()
  }, [load])

  // 重置:清空此頻道的 entry/trip(破壞性,限 owner)。完成後重新載入。
  const reset = useCallback(async () => {
    if (!channel) return
    if (!window.confirm(`確定清空頻道「${channel.name}」的所有條目與行程?此操作無法復原。`))
      return
    setLoading(true)
    setErr(null)
    try {
      await api.resetChannelData(cfg, channel.id)
      setEntries([])
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel?.id])

  if (!channel) {
    return (
      <div className="debug-list">
        <div style={{ color: '#6e6e78', padding: 16, textAlign: 'center' }}>
          先在左側進入一個頻道,這裡會顯示該頻道的 Entry 條目。
        </div>
      </div>
    )
  }

  return (
    <div className="debug-list">
      <div className="entries-head">
        <span>頻道 {channel.name} · {entries.length} 筆</span>
        <span style={{ display: 'flex', gap: 6 }}>
          <button onClick={load} disabled={loading}>
            {loading ? '…' : '↻ 重整'}
          </button>
          <button onClick={reset} disabled={loading} className="danger">
            🗑 重置
          </button>
        </span>
      </div>
      {err && <pre className="json-err">{err}</pre>}
      {!err && entries.length === 0 && !loading && (
        <div style={{ color: '#6e6e78', padding: 16, textAlign: 'center' }}>
          這個頻道還沒有 Entry。owner 記事(需 -llm want)後會出現。
        </div>
      )}
      {entries.map((e) => (
        <div key={e.id} className="entry-row">
          <span className="entry-when-mono">
            {e.start ? (e.startTime ? `${e.start} ${e.startTime}` : `${e.start} 全日`) : '(無時間)'}
            {e.end ? ` ~ ${e.endTime ? `${e.end} ${e.endTime}` : e.end}` : ''}
          </span>
          <span className="entry-item-mono">{e.title}</span>
          <span className="entry-id-mono">{e.id}</span>
        </div>
      ))}
    </div>
  )
}

function CallRow({ call }: { call: ApiCall }) {
  const [open, setOpen] = useState(false)

  // 狀態徽章:連線失敗(null)、2xx、其它。
  let statusClass = 'fail'
  let statusLabel = 'FAIL'
  if (call.status != null) {
    statusClass = call.ok ? 'ok' : 'err'
    statusLabel = String(call.status)
  }

  // 只顯示 path(去掉 base URL),列表才不會太長。
  let path = call.url
  try {
    const u = new URL(call.url)
    path = u.pathname + u.search
  } catch {
    /* 保留原字串 */
  }

  return (
    <div className="call">
      <div className="call-head" onClick={() => setOpen((o) => !o)}>
        <span className={`method ${call.method}`}>{call.method}</span>
        <span className="call-path">{path}</span>
        <span className={`status ${statusClass}`}>{statusLabel}</span>
        <span className="dur">{call.durationMs}ms</span>
      </div>
      {open && (
        <div className="call-body">
          <div className="kv-label">URL</div>
          <pre>{call.url}</pre>

          {call.requestBody != null && (
            <>
              <div className="kv-label">Request Body</div>
              <pre>{pretty(call.requestBody)}</pre>
            </>
          )}

          {call.error && (
            <>
              <div className="kv-label">連線錯誤</div>
              <pre className="json-err">{call.error}</pre>
            </>
          )}

          {call.status != null && (
            <>
              <div className="kv-label">
                Response · {call.status} · {call.durationMs}ms
              </div>
              {call.responseBody != null ? (
                <pre>{pretty(call.responseBody)}</pre>
              ) : (
                <pre className="json-err">
                  {call.responseText || '(空回應 / 非 JSON)'}
                </pre>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}

function pretty(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}
