import { useCallback, useEffect, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import {
  ChevronLeft,
  Users, Send, Share2, Copy, Check, Trash2,
} from 'lucide-react'
import type { ClientConfig, PresentedEntry } from './api'
import * as api from './api'
import type { Channel, ChannelRole, Entry, Member, Message, User } from './types'
import { listMessages, saveMessage } from './deviceDB'
import { Avatar, ErrorBanner, errMsg, isSubmitEnter, LS_DEFAULT_CHANNEL } from './App'
import { MultiTrackTimeline, type TaskPlaceholder } from './Timeline'

// 助手(assist 回答)的作者 ID,需與後端及 iOS ChatStore.assistantID 一致。
const ASSISTANT_ID = 'usr_assistant'

// 聊天訊息(後端 Message + 前端專用欄位)。
// presented:agent 用 present_entries 輸出、要在答案泡泡下用列表顯示的條目。
// pending:後端處理中的佔位泡泡,渲染海浪載入動畫(無文字),完成後就地替換。
type ChatMessage = Message & { presented?: PresentedEntry[]; pending?: boolean }

export function ChatScreen({
  cfg,
  channel,
  user,
  onBack,
}: {
  cfg: ClientConfig
  channel: Channel
  user: User
  onBack: () => void
}) {
  // owner 輸入=發訊息;成員輸入=語意查詢(回答顯示在訊息流,對齊 iOS App)。
  const isOwner = channel.ownerID === user.id
  const [messages, setMessages] = useState<ChatMessage[]>([])
  // Entry:LLM(record_entry 工具)從訊息解析出的條目,按 messageID 掛到對應訊息下方。
  const [entries, setEntries] = useState<Entry[]>([])
  // updatingEntryIDs:目前正在被 entry_update 工具更新的條目 ID,對應卡片顯示「更新中」光影動畫。
  // WS 收到 entry_updating 加入(並保證最短顯示 800ms),entries_updated(更新完成刷新)時清空。
  const [updatingEntryIDs, setUpdatingEntryIDs] = useState<Set<string>>(new Set())
  const [draft, setDraft] = useState('')
  const [lastDraft, setLastDraft] = useState('')
  const [inputFocused, setInputFocused] = useState(false)
  // ask_user:agent 缺資訊(如住宿退房日)時,透過 WS 推來的請求;非 null 時前端開對應 UI。
  const [askUser, setAskUser] = useState<{ askType: string; prompt: string } | null>(null)
  // taskPlaceholders:task_plan 建立中的任務,依 date 在時間軸插入佔位卡;
  // 對應 entry_add(帶 taskID)完成後由 task_entry_ready 移除。
  const [taskPlaceholders, setTaskPlaceholders] = useState<TaskPlaceholder[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  // 成員管理在頻道內開啟(對齊 iOS App 的聊天頁右上角入口)。
  const [showMembers, setShowMembers] = useState(false)
  // 分享彈窗
  const [showShare, setShowShare] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)
  const navbarRef = useRef<HTMLDivElement>(null)
  const lastScrollY = useRef(0)
  const todayRef = useRef<HTMLDivElement>(null as unknown as HTMLDivElement)

  useEffect(() => {
    const el = bodyRef.current
    const nav = navbarRef.current
    if (!el || !nav) return
    const onScroll = () => {
      const y = el.scrollTop
      const diff = y - lastScrollY.current
      lastScrollY.current = y
      if (diff > 4) {
        nav.classList.add('navbar-hidden')
      } else if (diff < -4) {
        nav.classList.remove('navbar-hidden')
      }
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  const load = useCallback(async () => {
    setErr(null)
    try {
      // 原話從「裝置端 DB」讀(與 server 隔離);entry/trip 從後端讀(僅 owner)。
      const [msgs, ents] = await Promise.all([
        listMessages(channel.id),
        isOwner ? api.fetchEntries(cfg, channel.id) : Promise.resolve([]),
      ])
      setMessages(isOwner ? [] : msgs)
      setEntries(ents)
    } catch (e) {
      setErr(errMsg(e))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel.id, isOwner])

  useEffect(() => {
    load()
  }, [load])

  // updatingSince:記每個更新中 entryID 的起始時間,用來保證「更新中」動畫最短顯示 800ms
  // (entry_update 後端很快完成,不設下限會一閃而過看不見)。
  const updatingSinceRef = useRef<Map<string, number>>(new Map())
  const MIN_UPDATING_MS = 800

  useEffect(() => {
    const base = cfg.baseURL.replace(/^http/, 'ws')
    // 瀏覽器原生 WebSocket API 不支援自訂 header,token 改用 query string 帶,
    // 供後端驗證是否為此頻道成員(見 server/internal/api/ws.go handleWS)。
    const tokenQS = cfg.token ? `?token=${encodeURIComponent(cfg.token)}` : ''
    const ws = new WebSocket(`${base}/v1/channels/${channel.id}/ws${tokenQS}`)
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data)
        if (msg.event === 'entry_updating' && msg.entryID) {
          // 條目開始更新:對應卡片亮起「更新中」動畫,並記錄起始時間。
          updatingSinceRef.current.set(msg.entryID, Date.now())
          setUpdatingEntryIDs((prev) => new Set(prev).add(msg.entryID))
        } else if (msg.event === 'entries_updated') {
          // 更新完成:重抓條目,並依最短顯示時間逐一解除「更新中」狀態。
          api.fetchEntries(cfg, channel.id).then(setEntries).catch(() => {})
          const now = Date.now()
          updatingSinceRef.current.forEach((since, id) => {
            const elapsed = now - since
            const clear = () => {
              updatingSinceRef.current.delete(id)
              setUpdatingEntryIDs((prev) => {
                const next = new Set(prev)
                next.delete(id)
                return next
              })
            }
            if (elapsed >= MIN_UPDATING_MS) clear()
            else setTimeout(clear, MIN_UPDATING_MS - elapsed)
          })
        } else if (msg.event === 'ask_user' && msg.askType) {
          // agent 缺資訊,請使用者透過 UI 補上;開對應輸入元件(目前支援 date)。
          setAskUser({ askType: msg.askType, prompt: msg.prompt ?? '' })
        } else if (msg.event === 'task_created' && typeof msg.taskID === 'number') {
          // task_plan 建立任務:在該日期下插入一張「新增中」佔位卡。
          setTaskPlaceholders((prev) => [...prev, { taskID: msg.taskID, date: msg.date ?? '', text: msg.text ?? '', kind: msg.kind ?? '' }])
        } else if (msg.event === 'task_entry_ready' && typeof msg.taskID === 'number') {
          // entry_add 已完成對應步驟:移除佔位卡,並重抓條目讓正式卡片出現。
          setTaskPlaceholders((prev) => prev.filter((p) => p.taskID !== msg.taskID))
          api.fetchEntries(cfg, channel.id).then(setEntries).catch(() => {})
        }
      } catch {}
    }
    return () => ws.close()
  }, [cfg.baseURL, cfg.token, channel.id])

  useEffect(() => {
    if (entries.length > 0 && todayRef.current && bodyRef.current) {
      const el = todayRef.current
      const body = bodyRef.current
      body.scrollTo({ top: el.offsetTop - 60, behavior: 'instant' })
    }
  }, [entries])

  // 本地訊息(不寫入後端,純前端顯示用):查詢的提問/回答泡泡。
  const mkLocalMsg = (
    id: string,
    authorID: string,
    authorName: string,
    text: string,
  ): ChatMessage => ({
    id, channelID: channel.id, authorID, authorName, text,
    createdAt: new Date().toISOString(),
  })

  // owner 用:統一輸入送進 assist,LLM 自主判斷記錄事項或回答提問。
  // overrideText:由 ask_user 回填等場景直接指定送出內容(不從 draft 取)。
  const send = async (overrideText?: string) => {
    const text = (overrideText ?? draft).trim()
    if (!text) return
    setSending(true)
    setErr(null)
    setLastDraft(text)
    if (overrideText === undefined) setDraft('') // 只清使用者手打的草稿;ask_user 回填不動 draft
    // 立刻插入處理中佔位泡泡(海浪動畫);完成後就地替換、失敗則移除。
    const pendingID = `pending_${Date.now()}`
    const pending = mkLocalMsg(pendingID, ASSISTANT_ID, '', '')
    pending.pending = true
    setMessages((prev) => [...prev, pending])
    const drop = () => setMessages((prev) => prev.filter((m) => m.id !== pendingID))
    // record 時 agent 非同步寫 entry,記下送出前的數量當基準,輪詢到變多才算寫完。
    const baseCount = entries.length
    try {
      const res = await api.assist(cfg, channel.id, text)
      if (res.kind === 'recorded') {
        // 記錄了 → 把原話存進「裝置端 DB」(原話的權威來源,與 server 隔離)。
        // res.text 為原話;後端不存原話,僅回它供前端落地裝置 DB。
        await saveMessage({
          id: `msg_${Date.now()}`,
          channelID: channel.id,
          authorID: user.id,
          authorName: user.name,
          text: res.text,
          createdAt: new Date().toISOString(),
        }).catch(() => {})
        // 波浪持續到 entry 真的寫入並顯示後才停。
        // 輪詢 fetchEntries 直到筆數比送出前多(agent 寫好);逾時則放棄等待先停。
        const deadline = Date.now() + 20000 // 上限 20s,避免 agent 卡住時無限轉
        let shown = false
        while (Date.now() < deadline) {
          await new Promise((r) => setTimeout(r, 1000))
          let next: Entry[]
          try {
            next = await api.fetchEntries(cfg, channel.id)
          } catch {
            continue // 暫時抓失敗就再試
          }
          if (next.length > baseCount) {
            setEntries(next) // entry 已顯示在最上方列表
            shown = true
            break
          }
        }
        if (!shown) {
          // 逾時沒等到新 entry:仍刷新一次列表(可能 agent 沒產生條目)。
          await api.fetchEntries(cfg, channel.id).then(setEntries).catch(() => {})
        }
        // 記錄了 → 原話已歸入上方 entry 卡,訊息流不保留這則原話泡泡(移除佔位)。
        // 對齊 iOS:記事原話存而不顯,內容由 entry 承載。
        drop()
      } else {
        // 回答了 → 佔位泡泡換成「提問 + 答案」兩個本地泡泡(不寫入頻道)。
        // 答案泡泡掛上 agent 用 present_entries 輸出的條目,前端用列表元件顯示。
        const ans = mkLocalMsg(`ans_${Date.now()}`, ASSISTANT_ID, '', res.answer)
        ans.presented = res.entries
        setMessages((prev) => [
          ...prev.filter((m) => m.id !== pendingID),
          mkLocalMsg(`ask_${Date.now()}`, user.id, user.name, text),
          ans,
        ])
      }
    } catch (e) {
      drop()
      setErr(errMsg(e))
      setDraft(text) // 失敗時還回草稿
    } finally {
      setSending(false)
    }
  }

  // 成員用:自然語言查詢頻道。問答持久化進裝置端 DB(重開頻道仍在,後端不存)。
  const ask = async () => {
    const q = draft.trim()
    if (!q) return
    setSending(true)
    setErr(null)
    setDraft('')
    // 提問泡泡(持久化)+ 處理中佔位泡泡(海浪動畫,暫態)。
    const askMsg = mkLocalMsg(`ask_${Date.now()}`, user.id, user.name, q)
    const pendingID = `pending_${Date.now()}`
    const pending = mkLocalMsg(pendingID, ASSISTANT_ID, '', '')
    pending.pending = true
    setMessages((prev) => [...prev, askMsg, pending])
    // 提問存裝置 DB。
    void saveMessage(askMsg).catch(() => {})
    try {
      const a = await api.semanticQuery(cfg, channel.id, q)
      // 佔位泡泡就地換成答案,並把答案也存進裝置 DB。
      const ansMsg = mkLocalMsg(`ans_${Date.now()}`, ASSISTANT_ID, '助手', a.answer)
      void saveMessage(ansMsg).catch(() => {})
      setMessages((prev) =>
        prev.map((m) => (m.id === pendingID ? ansMsg : m)),
      )
    } catch (e) {
      setMessages((prev) => prev.filter((m) => m.id !== pendingID))
      setErr(errMsg(e))
    } finally {
      setSending(false)
    }
  }

  // 頻道內的成員管理(對齊 iOS App:聊天頁 → 成員)。
  if (showMembers) {
    return (
      <MembersScreen
        cfg={cfg}
        channel={channel}
        isOwner={isOwner}
        onBack={() => setShowMembers(false)}
      />
    )
  }

  if (showShare) {
    return (
      <ShareModal
        cfg={cfg}
        channel={channel}
        isOwner={isOwner}
        onClose={() => setShowShare(false)}
      />
    )
  }

  return (
    <>
      <div className="navbar" ref={navbarRef}>
        <button className="btn icon-btn" onClick={onBack}>
          <ChevronLeft size={20} strokeWidth={1.8} />
        </button>
        <span className="title">{channel.name}</span>
        <ChannelMenu channelID={channel.id} />
        <div style={{ display: 'flex', gap: 2 }}>
          {isOwner && (
            <button className="btn icon-btn" onClick={() => setShowShare(true)} title="分享">
              <Share2 size={18} strokeWidth={1.8} />
            </button>
          )}
          <button className="btn icon-btn" onClick={() => setShowMembers(true)} title="成員">
            <Users size={18} strokeWidth={1.8} />
          </button>
        </div>
      </div>
      <div className="chat-area">
        <div className="screen-body" ref={bodyRef} onMouseDown={() => { setInputFocused(false); setMessages([]) }}>
          <ErrorBanner msg={err} />
          {entries.length === 0 && messages.length === 0 && !sending ? (
            <div className="empty">
              {isOwner ? '在下方輸入記事，會依時間排列在這裡。' : '在下方查詢頻道內容。'}
            </div>
          ) : entries.length > 0 ? (
            <MultiTrackTimeline entries={entries} todayRef={todayRef} updatingIDs={updatingEntryIDs} taskPlaceholders={taskPlaceholders} />
          ) : null}
        </div>

        {/* 浮層：訊息對話區，覆蓋在時間軸上方，毛玻璃背景 */}
        {(messages.length > 0 || sending || inputFocused) && (
          <div className="chat-overlay" onMouseDown={(e) => e.stopPropagation()}>
            <div className="chat-overlay-inner">
              {sending && lastDraft && (
                <div className="bubble-group mine">
                  <div className="bubble mine">
                    <div className="text">{lastDraft}</div>
                  </div>
                </div>
              )}
              {messages.map((m) => (
                <MessageBubble key={m.id} msg={m} meID={user.id} />
              ))}
            </div>
          </div>
        )}

        <div className="composer">
          <div className="composer-row">
            <input
              value={draft}
              placeholder={isOwner ? '記事或提問…' : '用自然語言查詢這個頻道…'}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => isSubmitEnter(e) && (isOwner ? send() : ask())}
              onFocus={() => setInputFocused(true)}
              onBlur={() => setInputFocused(false)}
            />
            <button
              onClick={isOwner ? () => send() : ask}
              disabled={sending || !draft.trim()}
            >
              <Send size={16} strokeWidth={2} />
            </button>
          </div>
        </div>
      </div>
      {askUser && (
        <AskUserSheet
          askType={askUser.askType}
          prompt={askUser.prompt}
          onCancel={() => setAskUser(null)}
          onSubmit={(value) => {
            setAskUser(null)
            // 把使用者選的值當成一則新訊息送回,agent 靠對話歷史接上前文(缺哪筆住宿的退房日)。
            send(value)
          }}
        />
      )}
    </>
  )
}

// AskUserSheet:agent 呼叫 ask_user 時,前端依 askType 開啟對應輸入 UI 的底部彈出面板。
// 目前支援 askType='date'(日期選擇器);使用者選定後把值透過 onSubmit 送回(當成新訊息)。
function AskUserSheet({
  askType,
  prompt,
  onSubmit,
  onCancel,
}: {
  askType: string
  prompt: string
  onSubmit: (value: string) => void
  onCancel: () => void
}) {
  const [value, setValue] = useState('')
  return (
    <div className="ask-user-backdrop" onClick={onCancel}>
      <div className="ask-user-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="ask-user-prompt">{prompt || '請補充資訊'}</div>
        {askType === 'date' ? (
          <input
            className="ask-user-date"
            type="date"
            value={value}
            autoFocus
            onChange={(e) => setValue(e.target.value)}
          />
        ) : (
          <div className="ask-user-unsupported">不支援的輸入類型：{askType}</div>
        )}
        <div className="ask-user-actions">
          <button className="btn-secondary" onClick={onCancel}>取消</button>
          <button
            className="btn-primary"
            disabled={!value}
            onClick={() => value && onSubmit(value)}
          >
            確定
          </button>
        </div>
      </div>
    </div>
  )
}

function MessageBubble({
  msg,
  meID,
}: {
  msg: ChatMessage
  meID: string
}) {
  // 新模型:message 只剩原話,LLM 標注已移至 entry(在上方事件列表顯示)。
  const mine = msg.authorID === meID
  // 只有助理「回答」訊息用 Markdown 渲染;使用者輸入/記事訊息維持純文字。
  const isAnswer = msg.authorID === ASSISTANT_ID
  return (
    <div className={`bubble-group ${mine ? 'mine' : ''}`}>
      <div className={`bubble ${mine ? 'mine' : ''} ${msg.pending ? 'pending' : ''}`}>
        {/* 助手回答泡泡不顯示作者名(不出現「助手」);對齊 iOS MessageRow */}
        {!mine && !isAnswer && msg.authorName && (
          <div className="sub" style={{ marginBottom: 2 }}>
            {msg.authorName}
          </div>
        )}
        {msg.pending ? (
          // 後端處理中:海浪載入動畫(色塊群組依序起伏)。
          <WaveLoader />
        ) : isAnswer ? (
          <div className="text markdown">
            <ReactMarkdown>{msg.text}</ReactMarkdown>
          </div>
        ) : (
          <div className="text">{msg.text}</div>
        )}
      </div>
      {/* agent 用 present_entries 輸出的條目,在答案泡泡下用列表顯示 */}
      {msg.presented?.map((e, i) => (
        <PresentedCard key={`p${i}`} entry={e} />
      ))}
    </div>
  )
}

// WaveLoader:後端處理中的海浪載入動畫。
// 多個色塊依序上下起伏(animation-delay 漸進),整體像海浪由左而右流動。
function WaveLoader() {
  const bars = 5
  return (
    <div className="wave" aria-label="處理中" role="status">
      {Array.from({ length: bars }, (_, i) => (
        <span
          key={i}
          className="wave-bar"
          style={{ animationDelay: `${i * 0.12}s` }}
        />
      ))}
    </div>
  )
}

// PresentedCard 顯示 present_entries 輸出的條目(查詢結果列表用)。
function PresentedCard({ entry }: { entry: PresentedEntry }) {
  const allDay = !entry.startTime
  const when = entry.start
    ? allDay ? entry.start : `${entry.start} ${entry.startTime}`
    : '未指定時間'
  const endLabel = entry.end
    ? entry.endTime ? ` ~ ${entry.end} ${entry.endTime}` : ` ~ ${entry.end}`
    : ''
  return (
    <div className="entry-card">
      <span className="entry-ico">📅</span>
      <div className="entry-body">
        <div className="entry-item">{entry.item}</div>
        <div className="entry-when">{when}{endLabel}</div>
      </div>
    </div>
  )
}

// ---- 成員頁 ----

function MembersScreen({
  cfg,
  channel,
  isOwner,
  onBack,
}: {
  cfg: ClientConfig
  channel: Channel
  isOwner: boolean
  onBack: () => void
}) {
  const [members, setMembers] = useState<Member[]>([])
  const [email, setEmail] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)

  const load = useCallback(async () => {
    setErr(null)
    try {
      setMembers(await api.fetchMembers(cfg, channel.id))
    } catch (e) {
      setErr(errMsg(e))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel.id])

  useEffect(() => {
    load()
  }, [load])

  // 以 email 邀請(對齊 iOS App);新成員預設 viewer(查詢權限)。
  const invite = async () => {
    const e = email.trim().toLowerCase()
    if (!e.includes('@')) return
    setAdding(true)
    setErr(null)
    try {
      setMembers(await api.addMember(cfg, channel.id, e, 'viewer'))
      setEmail('')
    } catch (err) {
      setErr(errMsg(err))
    } finally {
      setAdding(false)
    }
  }

  // owner 切換成員權限(editor ↔ viewer)。owner 自己不可改。
  const toggleRole = async (m: Member) => {
    if (m.id === channel.ownerID) return
    const next: ChannelRole = m.role === 'editor' ? 'viewer' : 'editor'
    setErr(null)
    try {
      setMembers(await api.setMemberRole(cfg, channel.id, m.id, next))
    } catch (err) {
      setErr(errMsg(err))
    }
  }

  return (
    <>
      <div className="navbar">
        <button className="btn icon-btn" onClick={onBack}>
          <ChevronLeft size={20} strokeWidth={1.8} />
        </button>
        <span className="title">成員</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body">
        <ErrorBanner msg={err} />
        <div className="section-title">頻道成員 · {channel.name}</div>
        <ul className="list">
          {members.map((m) => {
            const isChannelOwner = m.id === channel.ownerID
            const roleLabel = isChannelOwner ? '擁有者' : m.role === 'editor' ? '可修改' : '查詢'
            return (
              <li key={m.id} className="row">
                <Avatar user={m} />
                <div className="grow">
                  <div className="name">{m.name}</div>
                  <div className="sub">{m.id}</div>
                </div>
                {isOwner && !isChannelOwner ? (
                  <button className={`role-chip ${m.role}`} onClick={() => toggleRole(m)} title="點擊切換 修改/查詢 權限">
                    {roleLabel}
                  </button>
                ) : (
                  <span className={`role-chip ${isChannelOwner ? 'owner' : m.role} static`}>
                    {roleLabel}
                  </span>
                )}
              </li>
            )
          })}
        </ul>
        <div className="section-title">以 Email 邀請</div>
        <div className="field">
          <input
            value={email}
            type="email"
            autoComplete="email"
            placeholder="輸入對方的 Email 後按 Enter"
            onChange={(e) => setEmail(e.target.value)}
            onKeyDown={(e) => isSubmitEnter(e) && invite()}
          />
        </div>
        <div style={{ padding: '8px 16px 0' }}>
          <button className="btn-primary" onClick={invite} disabled={adding || !email.includes('@')}>
            {adding ? '邀請中…' : '邀請加入'}
          </button>
        </div>
      </div>
    </>
  )
}

// ---- 分享彈窗 ----

function ShareModal({
  cfg,
  channel,
  isOwner,
  onClose,
}: {
  cfg: ClientConfig
  channel: Channel
  isOwner: boolean
  onClose: () => void
}) {
  const [token, setToken] = useState<string | null>(null)
  const [editable, setEditable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const publicURL = token ? `${window.location.origin}/public/${token}` : null

  useEffect(() => {
    api.getPublicLink(cfg, channel.id)
      .then((r) => { setToken(r.linkToken); setEditable(r.editable) })
      .catch(() => setToken(null))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel.id])

  const generate = async () => {
    setLoading(true)
    setErr(null)
    try {
      const r = await api.createPublicLink(cfg, channel.id, editable)
      setToken(r.linkToken)
      setEditable(r.editable)
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setLoading(false)
    }
  }

  const toggleEditable = async (val: boolean) => {
    setEditable(val)
    try {
      const r = await api.createPublicLink(cfg, channel.id, val)
      setToken(r.linkToken)
      setEditable(r.editable)
    } catch (e) {
      setErr(errMsg(e))
    }
  }

  const revoke = async () => {
    setLoading(true)
    setErr(null)
    try {
      await api.deletePublicLink(cfg, channel.id)
      setToken(null)
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setLoading(false)
    }
  }

  const copy = () => {
    if (!publicURL) return
    navigator.clipboard.writeText(publicURL).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <>
      <div className="navbar">
        <button className="btn icon-btn" onClick={onClose}>
          <ChevronLeft size={20} strokeWidth={1.8} />
        </button>
        <span className="title">分享頻道</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body">
        <ErrorBanner msg={err} />
        <div className="section-title">公開連結</div>
        <div className="field" style={{ color: 'var(--ios-gray)', fontSize: 13 }}>
          任何人取得連結後即可查看此頻道的行程（無需登入）。
        </div>
        {loading ? (
          <div className="empty">載入中…</div>
        ) : token ? (
          <>
            <div className="share-link-box">
              <div className="share-link-url">{publicURL}</div>
              <button className="share-link-copy" onClick={copy} title="複製連結">
                {copied ? <Check size={16} strokeWidth={2} /> : <Copy size={16} strokeWidth={1.8} />}
              </button>
            </div>
            <div style={{ padding: '8px 16px 0' }}>
              <button className="btn-primary" onClick={copy}>
                {copied ? '✅ 已複製' : '複製連結'}
              </button>
            </div>
            {isOwner && (
              <>
                <div className="share-toggle-row">
                  <span className="share-toggle-label">允許訪客新增行程</span>
                  <label className="ios-toggle">
                    <input type="checkbox" checked={editable} onChange={(e) => toggleEditable(e.target.checked)} />
                    <span className="ios-toggle-slider" />
                  </label>
                </div>
                <div style={{ padding: '12px 16px 0' }}>
                  <button className="btn-danger" onClick={revoke}>
                    <Trash2 size={14} strokeWidth={1.8} style={{ marginRight: 6 }} />
                    撤銷連結
                  </button>
                </div>
              </>
            )}
          </>
        ) : (
          <>
            <div className="empty" style={{ padding: '24px 16px', textAlign: 'left' }}>
              尚未建立公開連結。
            </div>
            {isOwner && (
              <>
                <div className="share-toggle-row">
                  <span className="share-toggle-label">允許訪客新增行程</span>
                  <label className="ios-toggle">
                    <input type="checkbox" checked={editable} onChange={(e) => setEditable(e.target.checked)} />
                    <span className="ios-toggle-slider" />
                  </label>
                </div>
                <div style={{ padding: '8px 16px 0' }}>
                  <button className="btn-primary" onClick={generate}>
                    建立公開連結
                  </button>
                </div>
              </>
            )}
          </>
        )}
      </div>
    </>
  )
}

// ---- 頻道菜單(右上角設定) ----

function ChannelMenu({ channelID }: { channelID: string }) {
  const [open, setOpen] = useState(false)
  const defaultID = localStorage.getItem(LS_DEFAULT_CHANNEL)
  const isDefault = defaultID === channelID

  const setAsDefault = () => {
    localStorage.setItem(LS_DEFAULT_CHANNEL, channelID)
    setOpen(false)
  }

  const clearDefault = () => {
    localStorage.removeItem(LS_DEFAULT_CHANNEL)
    setOpen(false)
  }

  return (
    <div style={{ position: 'relative' }}>
      <button
        className="btn icon-btn"
        onClick={() => setOpen(!open)}
        title="頻道設定"
        style={{ padding: 0 }}
      >
        ⋯
      </button>
      {open && (
        <div
          style={{
            position: 'absolute',
            top: '100%',
            right: 0,
            background: 'var(--ios-white)',
            border: '1px solid var(--ios-separator)',
            borderRadius: 8,
            marginTop: 4,
            minWidth: 180,
            zIndex: 1000,
            boxShadow: '0 2px 10px rgba(0,0,0,0.1)',
          }}
        >
          {!isDefault ? (
            <button
              onClick={setAsDefault}
              style={{
                display: 'block',
                width: '100%',
                padding: '12px 16px',
                textAlign: 'left',
                border: 'none',
                background: 'none',
                cursor: 'pointer',
                fontSize: 14,
                color: 'var(--ios-link)',
                borderBottom: '1px solid var(--ios-separator)',
              }}
            >
              ✓ 設為目前行程
            </button>
          ) : (
            <button
              onClick={clearDefault}
              style={{
                display: 'block',
                width: '100%',
                padding: '12px 16px',
                textAlign: 'left',
                border: 'none',
                background: 'none',
                cursor: 'pointer',
                fontSize: 14,
                color: '#FF3B30',
              }}
            >
              ✗ 取消設定
            </button>
          )}
        </div>
      )}
    </div>
  )
}
