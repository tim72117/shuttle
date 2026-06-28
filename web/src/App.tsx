import { useCallback, useEffect, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import ReactMarkdown from 'react-markdown'
import {
  ChevronLeft,
  Users, Send, AlertCircle, Plus, LogIn, Share2, Copy, Check, Trash2,
} from 'lucide-react'
import type { ClientConfig, PresentedEntry } from './api'
import * as api from './api'
import { ApiError } from './api'
import type { Channel, ChannelRole, Entry, Member, Message, User } from './types'
import { listMessages, saveMessage } from './deviceDB'

// baseURL 是連線設定,跨分頁共用 → localStorage。
const LS_BASE = 'channel.baseURL'
// token / user 是「登入身分」,改用 sessionStorage:每個分頁獨立,
// 讓不同分頁能登入不同使用者(也為 per-session 鋪路)。
const SS_TOKEN = 'channel.token'
const SS_USER = 'channel.user'
const SS_EMAIL = 'channel.email'


// 聊天訊息(後端 Message + 前端專用欄位)。
// presented:agent 用 present_entries 輸出、要在答案泡泡下用列表顯示的條目。
// pending:後端處理中的佔位泡泡,渲染海浪載入動畫(無文字),完成後就地替換。
type ChatMessage = Message & { presented?: PresentedEntry[]; pending?: boolean }

export function useAppState() {
  const [baseURL, setBaseURL] = useState(() => {
    const origin = `${window.location.protocol}//${window.location.host}`
    const saved = localStorage.getItem(LS_BASE)
    // localhost 以外的環境（如 Cloud Run）忽略舊的 localhost 設定
    if (!saved || (saved.includes('localhost') && !origin.includes('localhost'))) {
      return origin
    }
    return saved
  })
  useEffect(() => localStorage.setItem(LS_BASE, baseURL), [baseURL])

  const [token, setToken] = useState<string | null>(
    () => sessionStorage.getItem(SS_TOKEN),
  )
  const [user, setUser] = useState<User | null>(() => {
    const raw = sessionStorage.getItem(SS_USER)
    return raw ? (JSON.parse(raw) as User) : null
  })
  const [email, setEmail] = useState<string>(
    () => sessionStorage.getItem(SS_EMAIL) ?? '',
  )

  const onAuthed = useCallback((tok: string, u: User, mail: string) => {
    sessionStorage.setItem(SS_TOKEN, tok)
    sessionStorage.setItem(SS_USER, JSON.stringify(u))
    sessionStorage.setItem(SS_EMAIL, mail)
    setToken(tok)
    setUser(u)
    setEmail(mail)
  }, [])

  const onLogout = useCallback(() => {
    sessionStorage.removeItem(SS_TOKEN)
    sessionStorage.removeItem(SS_USER)
    sessionStorage.removeItem(SS_EMAIL)
    setToken(null)
    setUser(null)
    setEmail('')
  }, [])

  const [activeChannel, setActiveChannel] = useState<Channel | null>(null)

  const cfg: ClientConfig = { baseURL, token }
  const effectiveUser = user ?? GUEST_USER

  return {
    cfg, activeChannel, setActiveChannel,
    baseURL, setBaseURL, token, setToken,
    user: effectiveUser, email, isGuest: user == null,
    onAuthed, onLogout,
  }
}

export function App() {
  const props = useAppState()
  // 偵測 /public/{token} 路徑，直接渲染公開分享頁
  const publicMatch = window.location.pathname.match(/^\/public\/([^/]+)$/)
  if (publicMatch) {
    return (
      <div className="web-app">
        <PublicViewScreen token={publicMatch[1]} />
      </div>
    )
  }
  return (
    <div className="web-app">
      <PhoneContent {...props} />
    </div>
  )
}

// 訪客身分(未登入),需與後端 guestUser 一致。
const GUEST_USER: User = { id: 'usr_me', name: '訪客', avatarColor: '#8e8e93' }

// 助手(assist 回答)的作者 ID,需與後端及 iOS ChatStore.assistantID 一致。
const ASSISTANT_ID = 'usr_assistant'


export interface ContentProps {
  cfg: ClientConfig
  activeChannel: Channel | null
  setActiveChannel: (c: Channel | null) => void
  baseURL: string
  setBaseURL: (s: string) => void
  token: string | null
  setToken: (t: string | null) => void
  user: User
  email: string
  isGuest: boolean
  onAuthed: (token: string, user: User, email: string) => void
  onLogout: () => void
}

export function PhoneContent(props: ContentProps) {
  const { cfg, activeChannel, setActiveChannel } = props
  const [inSettings, setInSettings] = useState(false)

  if (activeChannel) {
    return (
      <ChatScreen
        cfg={cfg}
        channel={activeChannel}
        user={props.user}
        onBack={() => setActiveChannel(null)}
      />
    )
  }

  if (inSettings) {
    return (
      <SettingsScreen
        cfg={props.cfg}
        baseURL={props.baseURL}
        setBaseURL={props.setBaseURL}
        user={props.user}
        email={props.email}
        isGuest={props.isGuest}
        onAuthed={props.onAuthed}
        onLogout={() => { props.onLogout(); setInSettings(false) }}
        onBack={() => setInSettings(false)}
      />
    )
  }

  return (
    <ChannelsScreen
      cfg={props.cfg}
      user={props.user}
      isGuest={props.isGuest}
      onAuthed={props.onAuthed}
      onOpen={(c) => setActiveChannel(c)}
      onOpenSettings={() => setInSettings(true)}
    />
  )
}


// ---- 共用小元件 ----

function Avatar({ user }: { user: { name: string; avatarColor: string } }) {
  return (
    <div className="avatar" style={{ background: user.avatarColor }}>
      {user.name.slice(0, 1)}
    </div>
  )
}

function ErrorBanner({ msg }: { msg: string | null }) {
  if (!msg) return null
  return <div className="banner"><AlertCircle size={14} strokeWidth={2} style={{verticalAlign: 'middle', marginRight: 6}} />{msg}</div>
}

// 統一把 API 錯誤轉成可顯示訊息。
function errMsg(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

// Enter 送出,但略過輸入法(注音/中日韓)組字中的 Enter——
// 組字選字時的 Enter 是「確認選字」,不該觸發送出。
function isSubmitEnter(e: ReactKeyboardEvent): boolean {
  // isComposing:組字進行中。keyCode 229:IME 處理中的按鍵。
  return e.key === 'Enter' && !e.nativeEvent.isComposing && e.keyCode !== 229
}

// ---- 頻道列表頁 ----

function ChannelsScreen({
  cfg,
  user,
  isGuest,
  onAuthed,
  onOpen,
  onOpenSettings,
}: {
  cfg: ClientConfig
  user: User
  isGuest: boolean
  onAuthed: (token: string, user: User, email: string) => void
  onOpen: (c: Channel) => void
  onOpenSettings: () => void
}) {
  const [channels, setChannels] = useState<Channel[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [showLogin, setShowLogin] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setErr(null)
    try {
      setChannels(await api.fetchChannels(cfg))
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token])

  useEffect(() => {
    load()
  }, [load])

  const submitCreate = async () => {
    const name = newName.trim()
    if (!name) return
    try {
      await api.createChannel(cfg, name)
      setNewName('')
      setCreating(false)
      load()
    } catch (e) {
      setErr(errMsg(e))
    }
  }

  return (
    <>
      <div className="navbar">
        <button className="btn icon-btn" onClick={() => setCreating((v) => !v)}>
          <Plus size={20} strokeWidth={1.8} />
        </button>
        <span className="title">頻道</span>
        <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          {isGuest ? (
            <button className="btn icon-btn" onClick={() => setShowLogin(v => !v)} title="登入">
              <LogIn size={18} strokeWidth={1.8} />
            </button>
          ) : (
            <button className="btn icon-btn" style={{ padding: 0 }} onClick={onOpenSettings} title="設定">
              <Avatar user={user} />
            </button>
          )}
        </div>
      </div>
      {showLogin && isGuest && (
        <div className="login-dropdown">
          <LoginForm baseURL={cfg.baseURL} onAuthed={(tok, u, mail) => {
            onAuthed(tok, u, mail)
            setShowLogin(false)
          }} />
        </div>
      )}
      {creating && (
        <div className="composer">
          <input
            autoFocus
            value={newName}
            placeholder="新頻道名稱…"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (isSubmitEnter(e)) submitCreate()
              if (e.key === 'Escape') {
                setCreating(false)
                setNewName('')
              }
            }}
          />
          <button onClick={submitCreate} disabled={!newName.trim()}>
            建立
          </button>
        </div>
      )}
      <div className="screen-body">
        <ErrorBanner msg={err} />
        {channels.length === 0 && !err ? (
          <div className="empty">
            {loading ? '載入中…' : '沒有頻道。按右上 ＋ 建立一個。'}
          </div>
        ) : (
          <ul className="list">
            {channels.map((c) => (
              <li key={c.id} className="row" onClick={() => onOpen(c)}>
                <Avatar user={{ name: c.name, avatarColor: 'var(--color-accent)' }} />
                <div className="grow">
                  <div className="name">
                    {c.name}
                    {c.ownerID === user.id && (
                      <span className="cat" style={{ marginLeft: 6 }}>我的</span>
                    )}
                  </div>
                  <div className="sub">
                    {c.lastMessagePreview ?? '尚無訊息'} · {c.memberCount} 人
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  )
}

// ---- 聊天頁 ----

function ChatScreen({
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
  const [draft, setDraft] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  // 成員管理在頻道內開啟(對齊 iOS App 的聊天頁右上角入口)。
  const [showMembers, setShowMembers] = useState(false)
  // 分享彈窗
  const [showShare, setShowShare] = useState(false)
  // 點選項目時顯示詳細資訊。
  const [selectedEntry, setSelectedEntry] = useState<Entry | null>(null)
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

  useEffect(() => {
    const base = cfg.baseURL.replace(/^http/, 'ws')
    const ws = new WebSocket(`${base}/v1/channels/${channel.id}/ws`)
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data)
        if (msg.event === 'entries_updated') {
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
    } else {
      bodyRef.current?.scrollTo(0, bodyRef.current.scrollHeight)
    }
  }, [messages, entries])

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
  const send = async () => {
    const text = draft.trim()
    if (!text) return
    setSending(true)
    setErr(null)
    setDraft('')
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

  // 點選項目時顯示詳細資訊。
  if (selectedEntry) {
    return (
      <EntryDetailModal
        entry={selectedEntry}
        onBack={() => setSelectedEntry(null)}
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
        <div className="screen-body" ref={bodyRef}>
          <ErrorBanner msg={err} />
          {/* LLM 回答泡泡(記事後 agent 回應) */}
          {messages.length > 0 && (
            <div className="chat-list">
              {messages.map((m) => (
                <MessageBubble key={m.id} msg={m} meID={user.id} />
              ))}
            </div>
          )}
          {/* Timeline:直接顯示，不需點 chip */}
          {entries.length === 0 && messages.length === 0 ? (
            <div className="empty">
              {isOwner ? '在下方輸入記事，會依時間排列在這裡。' : '在下方查詢頻道內容。'}
            </div>
          ) : entries.length > 0 ? (
            <MultiTrackTimeline entries={entries} todayRef={todayRef} />
          ) : null}
        </div>
        <div className="composer">
        <input
          value={draft}
          placeholder={isOwner ? '記事或提問…' : '用自然語言查詢這個頻道…'}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => isSubmitEnter(e) && (isOwner ? send() : ask())}
        />
        <button
          onClick={isOwner ? send : ask}
          disabled={sending || !draft.trim()}
        >
          <Send size={16} strokeWidth={2} />
        </button>
        </div>
      </div>
    </>
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
  const when = entry.start
    ? entry.allDay
      ? entry.start.slice(0, 10)
      : entry.start
    : '未指定時間'
  return (
    <div className="entry-card">
      <span className="entry-ico">📅</span>
      <div className="entry-body">
        <div className="entry-item">{entry.item}</div>
        <div className="entry-when">
          {when}
          {entry.end ? ` ~ ${entry.end}` : ''}
        </div>
      </div>
    </div>
  )
}


// ---- 時間軸頁 ----

// dayKey 取 entry 的日期部分(YYYY-MM-DD)作為分組鍵;無 start 歸「未指定時間」。
// ---- 跨度計算 ----


// ---- 工具函式 ----

function parseDateParts(d: string): { year: string; month: string; day: string } {
  const [year = '', month = '', day = ''] = d.split('-')
  return { year, month, day }
}

function entryTimeLabel(e: Entry): string {
  if (e.allDay || !e.start || e.start.length <= 10) return ''
  return e.start.slice(11)
}

function entrySpanLabel(e: Entry): string {
  if (!e.end || e.end === e.start) return ''
  const sd = e.start?.slice(0, 10) ?? ''
  const ed = e.end.slice(0, 10)
  if (sd === ed) {
    const t = e.end.length > 10 ? e.end.slice(11) : ''
    return t ? `~ ${t}` : ''
  }
  return `~ ${ed}`
}

// ---- 資料型別 ----

// 每一列的種類
type TLRow =
  | { kind: 'year';  key: string; label: string; accent: boolean }
  | { kind: 'month'; key: string; label: string; accent: boolean }
  | { kind: 'entry'; key: string; day: string; dayLabel: string | null; dot: 'main' | 'sub' | 'marker'; isBlank: boolean; isPad: boolean; lineTop: 'accent' | 'normal' | 'none'; lineBot: 'accent' | 'normal' | 'none'; card: { kind: 'main' | 'sub' | 'end'; entry: Entry } | null }

// ---- 建構函式 ----

function buildTLRows(entries: Entry[]): TLRow[] {
  const sorted = [...entries].sort((a, b) => (a.start ?? '').localeCompare(b.start ?? ''))

  // 1. 判斷主線
  // 主線條件：有結束時間且跨越不同日
  const mainSet = new Set(sorted.filter(e => {
    if (!e.end || e.end === e.start) return false
    return e.end.slice(0, 10) !== (e.start ?? '').slice(0, 10)
  }).map(e => e.id))
  const mainEntries = sorted.filter(e => mainSet.has(e.id))

  // 2. 某日是否在主線跨度內（用於畫橘線）
  function inMainSpan(day: string): boolean {
    return mainEntries.some(m => {
      const s = (m.start ?? '').slice(0, 10)
      const e = (m.end && m.end !== m.start ? m.end : m.start ?? '').slice(0, 10)
      return day >= s && day <= e
    })
  }

  // 3. 收集所有要顯示的天（entry 起始日 + 主線中間天 + 主線結束日 + 最後結束隔天）
  const daySet = new Set(sorted.map(e => e.start?.slice(0, 10) ?? '').filter(Boolean))
  let lastMainEnd = ''
  for (const m of mainEntries) {
    const s = (m.start ?? '').slice(0, 10)
    const e = (m.end && m.end !== m.start ? m.end : m.start ?? '').slice(0, 10)
    if (!s || !e) continue
    const d = new Date(s + 'T00:00:00')
    const endD = new Date(e + 'T00:00:00')
    while (d <= endD) { daySet.add(d.toISOString().slice(0, 10)); d.setDate(d.getDate() + 1) }
    if (e > lastMainEnd) lastMainEnd = e
  }
  if (lastMainEnd) {
    const after = new Date(lastMainEnd + 'T00:00:00')
    after.setDate(after.getDate() + 1)
    daySet.add(after.toISOString().slice(0, 10))
  }
  const days = [...daySet].sort()

  // 把主線結束標記當虛擬 entry，用 end 時間排入 sortedAll
  type VEntry = { id: string; sortKey: string; isEnd: boolean; source: Entry }
  const sortedAll: VEntry[] = sorted.map(e => ({ id: e.id, sortKey: e.start ?? '', isEnd: false, source: e }))
  for (const m of mainEntries) {
    const endStr = m.end && m.end !== m.start ? m.end : null
    if (endStr) sortedAll.push({ id: `end-${m.id}`, sortKey: endStr, isEnd: true, source: m })
  }
  sortedAll.sort((a, b) => a.sortKey.localeCompare(b.sortKey))

  // 4. 先把所有 entry 列（不含年月）按順序收集，再填線條
  type Pre = Omit<Extract<TLRow, { kind: 'entry' }>, 'lineTop' | 'lineBot'>
  const pre: Pre[] = []

  for (const day of days) {
    const { day: dayNum } = parseDateParts(day)
    const todayAll = sortedAll.filter(v => v.sortKey.slice(0, 10) === day)

    const dayRows: Pre[] = []

    if (todayAll.length === 0) {
      dayRows.push({ kind: 'entry', key: `day-${day}`, day, dayLabel: null, isBlank: true, isPad: false, dot: 'marker', card: null })
    } else {
      todayAll.forEach(v => {
        if (v.isEnd) {
          dayRows.push({ kind: 'entry', key: v.id, day, dayLabel: null, isBlank: false, isPad: false, dot: 'main', card: { kind: 'end', entry: v.source } })
        } else {
          dayRows.push({
            kind: 'entry', key: v.id, day, dayLabel: null, isBlank: false, isPad: false,
            dot: mainSet.has(v.id) ? 'main' : 'sub',
            card: { kind: mainSet.has(v.id) ? 'main' : 'sub', entry: v.source },
          })
        }
      })
    }

    // 中間天佔位列不顯示日期
    const isBlankDay = todayAll.length === 0
    if (dayRows.length > 0 && !isBlankDay) dayRows[0] = { ...dayRows[0], dayLabel: dayNum }
    dayRows.forEach(r => pre.push(r))
  }

  // 首尾各插一個灰色佔位列
  const firstDay = pre[0]?.day ?? ''
  const lastDay  = pre[pre.length - 1]?.day ?? ''
  const padRow = (day: string): typeof pre[0] => ({ kind: 'entry', key: `pad-${day}`, day, dayLabel: null, isBlank: false, isPad: true, dot: 'marker', card: null })
  const preWithPad = [padRow(firstDay), ...pre, padRow(lastDay)]

  // 5. 填線條
  const withLines = preWithPad.map((row, i): Extract<TLRow, { kind: 'entry' }> => {
    const cur  = !row.isPad && inMainSpan(row.day)
    const prev = i > 0 ? (!preWithPad[i - 1].isPad && inMainSpan(preWithPad[i - 1].day)) : false
    const next = i < preWithPad.length - 1 ? (!preWithPad[i + 1].isPad && inMainSpan(preWithPad[i + 1].day)) : false
    return {
      ...row,
      lineTop: i === 0 ? 'none' : (cur || prev) ? 'accent' : 'normal',
      lineBot: i === preWithPad.length - 1 ? 'none' : (cur && next) ? 'accent' : 'normal',
    }
  })

  // 6. 逐列輸出：遇到年/月變化先插年月列，再插 entry 列
  const rows: TLRow[] = []
  let prevYear = '', prevMonth = ''

  for (const row of withLines) {
    const { year, month } = parseDateParts(row.day)
    const acc = !row.isPad && !row.isBlank && inMainSpan(row.day)
    if (year !== prevYear) {
      rows.push({ kind: 'year', key: `year-${row.day}`, label: year, accent: acc })
      prevYear = year; prevMonth = ''
    }
    if (month !== prevMonth) {
      rows.push({ kind: 'month', key: `month-${row.day}`, label: `${month}月`, accent: acc })
      prevMonth = month
    }
    rows.push(row)
  }

  return rows
}

// ---- 純渲染元件 ----

function MultiTrackTimeline({ entries, todayRef }: { entries: Entry[], todayRef?: React.RefObject<HTMLDivElement> }) {
  const rows = buildTLRows(entries)
  const today = new Date().toISOString().slice(0, 10)
  let todayAttached = false
  return (
    <div className="tl-grid">
      {rows.map(row => {
        if (row.kind === 'year') return (
          <div key={row.key} className="tl-grid-row">
            <div className="tl-col-label tl-year-label">{row.label}</div>
            <div className="tl-col-axis">
              <div className={`tl-vline top${row.accent ? ' accent' : ''}`} />
              <div className={`tl-vline bot${row.accent ? ' accent' : ''}`} />
            </div>
            <div className="tl-col-card" />
          </div>
        )
        if (row.kind === 'month') return (
          <div key={row.key} className="tl-grid-row">
            <div className="tl-col-label tl-month-label">{row.label}</div>
            <div className="tl-col-axis">
              <div className={`tl-vline top${row.accent ? ' accent' : ''}`} />
              <div className={`tl-vline bot${row.accent ? ' accent' : ''}`} />
            </div>
            <div className="tl-col-card" />
          </div>
        )
        // entry row
        const { dot, lineTop, lineBot, card, dayLabel, isBlank } = row
        const rowDate = row.day ?? ''
        const isTodayAnchor = !todayAttached && todayRef && rowDate >= today && !isBlank
        if (isTodayAnchor) todayAttached = true
        return (
          <div key={row.key} ref={isTodayAnchor ? todayRef : undefined} className={`tl-grid-row${isBlank && !row.isPad ? ' blank' : ''}`}>
            {/* 日欄 */}
            <div className="tl-col-label">
              {dayLabel && <span className="tl-date-day">{dayLabel}</span>}
            </div>
            {/* 軸線欄：絕對線 + 置中點 */}
            <div className="tl-col-axis">
              {lineTop !== 'none' && <div className={`tl-vline top${lineTop === 'accent' ? ' accent' : ''}`} />}
              {lineBot !== 'none' && <div className={`tl-vline bot${lineBot === 'accent' ? ' accent' : ''}`} />}
              {isBlank && !row.isPad
                ? <div className="tl-dot-blank" />
                : <div className={dot === 'main' ? 'tl-dot-main' : dot === 'sub' ? 'tl-dot-sub' : 'tl-dot-day'} />
              }
            </div>
            {/* 卡片欄 */}
            <div className="tl-col-card">
              {card?.kind === 'main' && <MainCard entry={card.entry} />}
              {card?.kind === 'sub'  && <SubCard  entry={card.entry} />}
              {card?.kind === 'end'  && <EndCard  entry={card.entry} />}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function PinIcon() {
  return (
    <svg width="9" height="12" viewBox="0 0 10 14" fill="none" xmlns="http://www.w3.org/2000/svg" style={{ display: 'inline-block', verticalAlign: 'middle', marginRight: 2 }}>
      <path d="M5 0C2.24 0 0 2.24 0 5c0 3.75 5 9 5 9s5-5.25 5-9c0-2.76-2.24-5-5-5z" fill="currentColor"/>
      <circle cx="5" cy="5" r="2" fill="white"/>
    </svg>
  )
}

function NavButton({ location, lat, lng }: { location: string; lat?: number | null; lng?: number | null }) {
  const url = (lat != null && lng != null)
    ? `https://www.google.com/maps/search/?api=1&query=${lat},${lng}`
    : `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(location)}`
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" className="tl-nav-btn" title="開始導航">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
        <path d="M2 12L22 2L12 22L9 13L2 12Z" />
      </svg>
    </a>
  )
}

function MainCard({ entry }: { entry: Entry }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="tl-main-card tl-card-row" onClick={() => setOpen(o => !o)} style={{ cursor: 'pointer' }}>
      <div className="tl-card-content">
        <div className="tl-item">
          <span className="tl-main-title">{entry.item}</span>
        </div>
        {entry.location && <div className="entry-loc"><PinIcon /> {entry.location}</div>}
        <div className={`tl-card-expand${open ? ' open' : ''}`}>
          <div className="tl-card-expand-inner">
            {entry.summary && <div className="tl-expand-summary">{entry.summary}</div>}
            <div className="tl-expand-row">
              <span className="tl-expand-label">開始</span>
              <span>{entry.start ?? '—'}</span>
            </div>
            {entry.end && <div className="tl-expand-row">
              <span className="tl-expand-label">結束</span>
              <span>{entry.end}</span>
            </div>}
          </div>
        </div>
      </div>
      {entry.location && <NavButton location={entry.location} lat={entry.lat} lng={entry.lng} />}
    </div>
  )
}

function EndCard({ entry }: { entry: Entry }) {
  return (
    <div className="tl-end-card">
      <span className="tl-end-label">{entry.item} 結束</span>
    </div>
  )
}

function SubCard({ entry }: { entry: Entry }) {
  const [open, setOpen] = useState(false)
  const time = entryTimeLabel(entry)
  const span = entrySpanLabel(entry)
  const hasExpand = !!(entry.summary || entry.end)
  return (
    <div className={`tl-card tl-card-row${span ? ' tl-card-span' : ''}`}
      onClick={() => hasExpand && setOpen(o => !o)}
      style={hasExpand ? { cursor: 'pointer' } : undefined}>
      <div className="tl-card-content">
        <div className="tl-item">
          {time && <span className="tl-time">{time}</span>}
          {entry.item}
          {span && <span className="tl-span">{span}</span>}
        </div>
        {entry.location && <div className="entry-loc"><PinIcon /> {entry.location}</div>}
        {(entry.category || (entry.tags ?? []).length > 0) && (
          <div className="meta">
            {entry.category && <span className="cat">{entry.category}</span>}
            {(entry.tags ?? []).map(t => <span key={t} className="tag">#{t}</span>)}
          </div>
        )}
        <div className={`tl-card-expand${open ? ' open' : ''}`}>
          <div className="tl-card-expand-inner">
            {entry.summary && <div className="tl-expand-summary">{entry.summary}</div>}
            {entry.end && <div className="tl-expand-row">
              <span className="tl-expand-label">結束</span>
              <span>{entry.end}</span>
            </div>}
          </div>
        </div>
      </div>
      {entry.location && <NavButton location={entry.location} lat={entry.lat} lng={entry.lng} />}
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
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const publicURL = token
    ? `${window.location.origin}/public/${token}`
    : null

  useEffect(() => {
    api.getPublicLink(cfg, channel.id)
      .then(setToken)
      .catch(() => setToken(null))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel.id])

  const generate = async () => {
    setLoading(true)
    setErr(null)
    try {
      const t = await api.createPublicLink(cfg, channel.id)
      setToken(t)
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setLoading(false)
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
          任何人取得連結後即可查看此頻道的行程（唯讀，無需登入）。
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
              <div style={{ padding: '12px 16px 0' }}>
                <button className="btn-danger" onClick={revoke}>
                  <Trash2 size={14} strokeWidth={1.8} style={{ marginRight: 6 }} />
                  撤銷連結
                </button>
              </div>
            )}
          </>
        ) : (
          <>
            <div className="empty" style={{ padding: '24px 16px', textAlign: 'left' }}>
              尚未建立公開連結。
            </div>
            {isOwner && (
              <div style={{ padding: '0 16px' }}>
                <button className="btn-primary" onClick={generate}>
                  建立公開連結
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </>
  )
}

// ---- 公開分享頁（/public/{token}，無需登入） ----

function PublicViewScreen({ token }: { token: string }) {
  const [data, setData] = useState<{ channelID: string; trips: import('./types').Trip[]; entries: Entry[] } | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const todayRef = useRef<HTMLDivElement>(null as unknown as HTMLDivElement)
  const bodyRef = useRef<HTMLDivElement>(null)

  // 公開頁不依賴登入設定，直接用 localStorage 的 baseURL；
  // 若與前端同源（正式部署）或使用者未設定過，則 fallback 到 same-origin fetch。
  const resolvedBase = (() => {
    const saved = localStorage.getItem('channel.baseURL')
    if (saved && !saved.includes(window.location.host)) return saved
    return window.location.origin
  })()

  useEffect(() => {
    api.fetchPublicView(resolvedBase, token)
      .then(setData)
      .catch((e) => setErr(errMsg(e)))
      .finally(() => setLoading(false))
  }, [resolvedBase, token])

  useEffect(() => {
    if (data && todayRef.current && bodyRef.current) {
      bodyRef.current.scrollTo({ top: todayRef.current.offsetTop - 60, behavior: 'instant' })
    }
  }, [data])

  return (
    <>
      <div className="navbar">
        <span style={{ width: 36 }} />
        <span className="title">{data?.trips?.[0]?.title ?? '行程'}</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body" ref={bodyRef}>
        {loading && <div className="empty">載入中…</div>}
        {err && <div className="banner"><AlertCircle size={14} strokeWidth={2} style={{ verticalAlign: 'middle', marginRight: 6 }} />{err}</div>}
        {data && (
          data.entries.length === 0
            ? <div className="empty">此頻道尚無行程。</div>
            : <MultiTrackTimeline entries={data.entries} todayRef={todayRef} />
        )}
      </div>
    </>
  )
}

// ---- 設定頁(連線設定 + 測試 health) ----

function SettingsScreen({
  cfg,
  baseURL,
  setBaseURL,
  user,
  email,
  isGuest,
  onAuthed,
  onLogout,
  onBack,
}: {
  cfg: ClientConfig
  baseURL: string
  setBaseURL: (s: string) => void
  user: User
  email: string
  isGuest: boolean
  onAuthed: (token: string, user: User, email: string) => void
  onLogout: () => void
  onBack?: () => void
}) {
  const [health, setHealth] = useState<string>('未測試')

  const ping = async () => {
    setHealth('測試中…')
    try {
      const r = await api.health(cfg)
      setHealth(`✅ ${r.status}`)
    } catch (e) {
      setHealth(`❌ ${errMsg(e)}`)
    }
  }

  return (
    <>
      <div className="navbar">
        {onBack ? (
          <button className="btn icon-btn" onClick={onBack}>
            <ChevronLeft size={20} strokeWidth={1.8} />
          </button>
        ) : (
          <span style={{ width: 36 }} />
        )}
        <span className="title">設定</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body">
        {isGuest ? (
          <>
            <div className="section-title">目前身分</div>
            <div className="row">
              <Avatar user={user} />
              <div className="grow">
                <div className="name">訪客</div>
                <div className="sub">登入後發送的訊息會以你的身分顯示</div>
              </div>
            </div>
            <LoginForm baseURL={baseURL} onAuthed={onAuthed} />
          </>
        ) : (
          <>
            <div className="section-title">目前登入</div>
            <div className="row">
              <Avatar user={user} />
              <div className="grow">
                <div className="name">{user.name}</div>
                <div className="sub">{email || user.id}</div>
              </div>
            </div>
            <div className="row" onClick={onLogout}>
              <div className="grow">
                <div className="name" style={{ color: 'var(--ios-red)' }}>登出</div>
              </div>
              <ChevronLeft size={16} strokeWidth={1.5} color="#c7c7cc" style={{ transform: 'rotate(180deg)' }} />
            </div>
            <div className="section-title">API Token (CLI 用)</div>
            <TokenDisplay token={cfg.token} />
          </>
        )}
        <div className="section-title">後端連線</div>
        <div className="field">
          <label>Base URL</label>
          <input
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            placeholder="http://localhost:8080"
          />
        </div>
        <div className="section-title">健康檢查</div>
        <div className="row" onClick={ping}>
          <div className="grow">
            <div className="name">GET /health</div>
            <div className="sub">{health}</div>
          </div>
          <ChevronLeft size={16} strokeWidth={1.5} color="#c7c7cc" style={{ transform: 'rotate(180deg)' }} />
        </div>
        <div className="section-title">說明</div>
        <div className="field" style={{ color: 'var(--ios-gray)', fontSize: 13 }}>
          開發用後端測試台。登入身分存於 sessionStorage(分頁獨立),
          不同分頁可登入不同使用者。右側 debug panel 記錄每次 API 交易。
        </div>
      </div>
    </>
  )
}

// ---- 登入表單(內嵌於設定頁,訪客可登入 / 註冊) ----

function LoginForm({
  baseURL,
  onAuthed,
}: {
  baseURL: string
  onAuthed: (token: string, user: User, email: string) => void
}) {
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [email, setEmail] = useState('alice@channel.dev')
  const [password, setPassword] = useState('password')
  const [name, setName] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const cfg: ClientConfig = { baseURL, token: null }

  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      const res =
        mode === 'login'
          ? await api.login(cfg, email.trim(), password)
          : await api.register(cfg, email.trim(), password, name.trim())
      onAuthed(res.token, res.user, res.profile.email)
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className="section-title">{mode === 'login' ? '登入' : '註冊'}</div>
      <div className="field">
        <label>Email</label>
        <input
          value={email}
          type="email"
          autoComplete="email"
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@example.com"
        />
      </div>
      <div className="field">
        <label>密碼</label>
        <input
          type="password"
          value={password}
          autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
          onChange={(e) => setPassword(e.target.value)}
          onKeyDown={(e) => isSubmitEnter(e) && submit()}
          placeholder="至少 6 字元"
        />
      </div>
      {mode === 'register' && (
        <div className="field">
          <label>顯示名稱(可選)</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="留空則用 email"
          />
        </div>
      )}
      <ErrorBanner msg={err} />
      <div style={{ padding: '0 16px 8px' }}>
        <button
          className="btn-primary"
          onClick={submit}
          disabled={busy || !email.trim() || !password}
        >
          {busy ? '處理中…' : mode === 'login' ? '登入' : '註冊並登入'}
        </button>
        <div style={{ textAlign: 'center', marginTop: 14, fontSize: 14 }}>
          <span style={{ color: 'var(--ios-gray)' }}>
            {mode === 'login' ? '還沒有帳號?' : '已有帳號?'}
          </span>{' '}
          <span
            style={{ color: 'var(--color-accent)', cursor: 'pointer' }}
            onClick={() => {
              setMode(mode === 'login' ? 'register' : 'login')
              setErr(null)
            }}
          >
            {mode === 'login' ? '註冊' : '登入'}
          </span>
        </div>
      </div>
      <div
        className="field"
        style={{ color: 'var(--ios-gray)', fontSize: 13 }}
      >
        開發測試:可用 seed 帳號 alice@channel.dev / password
        (另有 bob、carol、dave)。或註冊新帳號。
      </div>
    </>
  )
}

// 項目詳細資訊彈窗。
function EntryDetailModal({
  entry,
  onBack,
}: {
  entry: Entry
  onBack: () => void
}) {
  const when = entry.start
    ? entry.allDay
      ? entry.start.slice(0, 10)
      : entry.start
    : '未指定時間'

  return (
    <>
      <div className="navbar">
        <button className="btn" onClick={onBack}>
          ‹ 返回
        </button>
        <span className="title">項目詳情</span>
        <span className="btn" style={{ visibility: 'hidden' }} />
      </div>
      <div className="screen-body" style={{ padding: '16px' }}>
        <div style={{ marginBottom: 24 }}>
          <div style={{ fontSize: 12, color: 'var(--ios-gray)', marginBottom: 4 }}>
            名稱
          </div>
          <div style={{ fontSize: 16, fontWeight: 600, marginBottom: 16 }}>
            {entry.item}
          </div>

          {entry.location && (
            <>
              <div style={{ fontSize: 12, color: 'var(--ios-gray)', marginBottom: 4 }}>
                地點
              </div>
              <div style={{ fontSize: 16, marginBottom: 16 }}>
                📍 {entry.location}
              </div>
            </>
          )}

          <div style={{ fontSize: 12, color: 'var(--ios-gray)', marginBottom: 4 }}>
            時間
          </div>
          <div style={{ fontSize: 16, marginBottom: 16 }}>
            🕐 {when}
            {entry.end ? ` ~ ${entry.end}` : ''}
          </div>

          {entry.summary && (
            <>
              <div style={{ fontSize: 12, color: 'var(--ios-gray)', marginBottom: 4 }}>
                摘要
              </div>
              <div style={{ fontSize: 14, color: '#666', marginBottom: 16 }}>
                {entry.summary}
              </div>
            </>
          )}

          {(entry.category || (entry.tags && entry.tags.length > 0)) && (
            <>
              <div style={{ fontSize: 12, color: 'var(--ios-gray)', marginBottom: 4 }}>
                標籤
              </div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {entry.category && (
                  <span
                    style={{
                      display: 'inline-block',
                      padding: '4px 10px',
                      background: '#e8f0ff',
                      color: '#007aff',
                      borderRadius: 6,
                      fontSize: 12,
                      fontWeight: 500,
                    }}
                  >
                    {entry.category}
                  </span>
                )}
                {entry.tags?.map((t) => (
                  <span
                    key={t}
                    style={{
                      display: 'inline-block',
                      padding: '4px 10px',
                      background: '#f2f2f7',
                      color: '#666',
                      borderRadius: 6,
                      fontSize: 12,
                    }}
                  >
                    #{t}
                  </span>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </>
  )
}

function TokenDisplay({ token }: { token: string | null }) {
  const [copied, setCopied] = useState(false)

  const copyToken = () => {
    if (token) {
      navigator.clipboard.writeText(token).then(() => {
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
      })
    }
  }

  if (!token) return null

  const displayToken = token.substring(0, 20) + '...' + token.substring(token.length - 10)

  return (
    <>
      <div className="token-box">{displayToken}</div>
      <div style={{ padding: '0 16px 12px' }}>
        <button className={`btn-secondary${copied ? ' success' : ''}`} onClick={copyToken}>
          {copied ? '✅ 已複製' : '複製 Token'}
        </button>
      </div>
    </>
  )
}
