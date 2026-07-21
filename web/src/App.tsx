import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent as ReactKeyboardEvent } from 'react'
import {
  ChevronLeft, ChevronDown, Check,
  Send, AlertCircle, Plus, LogIn, X, Settings, LogOut,
  List, Calendar, Sparkles, GalleryHorizontal, Map,
} from 'lucide-react'
import type { ClientConfig } from './api'
import * as api from './api'
import { ApiError } from './api'
import type { Channel, Entry, User } from './types'
import { LandingPage } from './LandingPage'
import { ChatScreen } from './ChatScreen'
import type { DesktopTimelineMirror } from './ChatScreen'
import { MultiTrackTimeline, type TaskPlaceholder } from './Timeline'
import type { AssistLang } from './assistLang'
import { ASSIST_LANG_KEY, getAssistLang } from './assistLang'
import { RecommendedPlacesList, RecommendedPlacesRow, FAKE_RECOMMENDED_PLACES } from './RecommendedPlaces'
import { RecommendedPlacesMap } from './RecommendedPlacesMap'

// baseURL 由建置時的 VITE_API_BASE 決定(見 .env.development),不開放使用者於 UI 修改;
// 未設時退回目前頁面 origin(production 前後端同源部署)。
export const BASE_URL: string =
  import.meta.env.VITE_API_BASE || `${window.location.protocol}//${window.location.host}`
// 默認頻道 ID (用戶設定的「開啟時自動進入」)
export const LS_DEFAULT_CHANNEL = 'tripace.defaultChannelID'
// 登入身分存 localStorage:跨分頁共用同一身分(一般網站慣例)。
const AUTH_TOKEN_KEY = 'tripace.auth.token'
const AUTH_USER_KEY = 'tripace.auth.user'
const AUTH_EMAIL_KEY = 'tripace.auth.email'

export function useAppState() {
  const [token, setToken] = useState<string | null>(
    () => localStorage.getItem(AUTH_TOKEN_KEY),
  )
  const [user, setUser] = useState<User | null>(() => {
    const raw = localStorage.getItem(AUTH_USER_KEY)
    return raw ? (JSON.parse(raw) as User) : null
  })
  const [email, setEmail] = useState<string>(
    () => localStorage.getItem(AUTH_EMAIL_KEY) ?? '',
  )

  const onAuthed = useCallback((tok: string, u: User, mail: string) => {
    localStorage.setItem(AUTH_TOKEN_KEY, tok)
    localStorage.setItem(AUTH_USER_KEY, JSON.stringify(u))
    localStorage.setItem(AUTH_EMAIL_KEY, mail)
    setToken(tok)
    setUser(u)
    setEmail(mail)
  }, [])

  const onLogout = useCallback(() => {
    localStorage.removeItem(AUTH_TOKEN_KEY)
    localStorage.removeItem(AUTH_USER_KEY)
    localStorage.removeItem(AUTH_EMAIL_KEY)
    setToken(null)
    setUser(null)
    setEmail('')
  }, [])

  const [activeChannel, setActiveChannel] = useState<Channel | null>(null)

  const cfg: ClientConfig = { baseURL: BASE_URL, token }
  const effectiveUser = user ?? GUEST_USER

  return {
    cfg, activeChannel, setActiveChannel,
    token, setToken,
    user: effectiveUser, email, isGuest: user == null,
    onAuthed, onLogout,
  }
}

// 桌面版斷點,需與 styles.css 的 @media (min-width: 768px) 一致。
const DESKTOP_BREAKPOINT = 768

// useIsDesktop:用 matchMedia 判斷目前寬度是否達到桌面斷點。
// 用 JS 判斷、只渲染其中一種佈局(而非兩份 DOM 都渲染、用 CSS 切換顯示),
// 是因為 ChatScreen 掛載時會建立 WebSocket 連線並各自 fetch 資料——
// 若手機版與桌面版兩棵 DOM 同時存在,選中頻道時會同時掛載兩個 ChatScreen,
// 造成重複連線與重複請求。
function useIsDesktop(): boolean {
  const [isDesktop, setIsDesktop] = useState(
    () => window.matchMedia(`(min-width: ${DESKTOP_BREAKPOINT}px)`).matches,
  )
  useEffect(() => {
    const mql = window.matchMedia(`(min-width: ${DESKTOP_BREAKPOINT}px)`)
    const onChange = () => setIsDesktop(mql.matches)
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [])
  return isDesktop
}

export function App({ isDemo = false }: { isDemo?: boolean } = {}) {
  const props = useAppState()
  // 根路徑渲染產品介紹 landing page(全寬,不套 phone 外框)
  if (window.location.pathname === '/') {
    return <LandingPage />
  }
  // 偵測 /public/{token} 路徑，直接渲染公開分享頁
  const publicMatch = window.location.pathname.match(/^\/public\/([^/]+)$/)
  if (publicMatch) {
    return (
      <div className="web-app">
        <PublicViewScreen token={publicMatch[1]} />
      </div>
    )
  }
  // /app 路徑:開發測試台本體(套 iPhone 外框)
  return (
    <div className="web-app">
      <PhoneContent {...props} isDemo={isDemo} />
    </div>
  )
}

// 訪客身分(未登入),需與後端 guestUser 一致。
const GUEST_USER: User = { id: 'usr_me', name: '訪客', avatarColor: '#8C7B6A' }

export interface ContentProps {
  cfg: ClientConfig
  activeChannel: Channel | null
  setActiveChannel: (c: Channel | null) => void
  token: string | null
  setToken: (t: string | null) => void
  user: User
  email: string
  isGuest: boolean
  onAuthed: (token: string, user: User, email: string) => void
  onLogout: () => void
  // isDemo:網址帶 ?demo 時為 true,只影響桌面版 DesktopRail 是否多顯示試做用
  // 導覽項目(見 DesktopRail)。手機版完全不讀這個值,行為不受影響。可選是因為
  // DebugApp.tsx 的 PhoneContent 用法（demoMode==='app' 分支）不涉及 ?demo 邏輯，
  // 不需要跟著補這個 prop。
  isDemo?: boolean
}

export function PhoneContent(props: ContentProps) {
  const { cfg, activeChannel, setActiveChannel } = props
  const [inSettings, setInSettings] = useState(false)
  // 寬度 >= 768px:改走桌面版佈局(側欄 + 主要區塊)。登入前不分寬度,一律走下面的
  // 登入畫面(登入前沒有頻道/聊天可看,不必特地做桌面版登入版面)。
  const isDesktop = useIsDesktop()

  if (props.isGuest) {
    return (
      <div className="login-screen">
        <div className="login-card">
          <div className="login-card-header">
            <div className="login-card-title">歡迎使用 Tripace</div>
            <div className="login-card-subtitle">請先登入或註冊帳號,才能查看與使用行程功能。</div>
          </div>
          <LoginForm baseURL={cfg.baseURL} onAuthed={props.onAuthed} />
        </div>
      </div>
    )
  }

  if (isDesktop) {
    return <DesktopContent {...props} />
  }

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

// ---- 桌面版佈局(寬度 >= 768px):左側邊欄(頻道列表 + 使用者選單)+ 右側 ChatScreen ----

// PanelMode:side panel 目前顯示的內容;null 代表收合(主區全寬)。
// 'demo-cards'/'demo-row'/'demo-map':試做用的推薦景點呈現方式(假資料,見
// RecommendedPlaces.tsx/RecommendedPlacesMap.tsx),只有網址帶 ?demo 時 rail 上
// 才會出現對應按鈕(見 DesktopRail),與正式的 channels/timeline 分開命名以便一眼區分。
type PanelMode = 'channels' | 'timeline' | 'demo-cards' | 'demo-row' | 'demo-map' | null

// 時間軸鏡像資料的初始值(尚未收到 ChatScreen 鏡像前,或未選擇行程時使用)。
const EMPTY_TIMELINE_MIRROR: DesktopTimelineMirror = {
  entries: [],
  updatingEntryIDs: new Set<string>(),
  taskPlaceholders: [] as TaskPlaceholder[],
  refetchEntries: () => {},
}

function DesktopContent(props: ContentProps) {
  const { cfg, activeChannel, setActiveChannel } = props
  // settingsOpen 獨立於 DesktopUserMenu 內部的 popover 開關狀態:選單裡點「設定」
  // 時會同時關閉 popover(DesktopUserMenu 內部 state)並開啟這裡的 dialog。
  // dialog 提升到這一層(而非渲染在 DesktopUserMenu/側欄內部)渲染,是因為
  // .desktop-layout 設有 overflow: hidden,side bar 寬度也只有 272px——
  // 若 dialog 渲染在側欄內部,置中/覆蓋全畫面的彈窗會被側欄裁切或擠壓變形。
  // 提升到這裡、和 .desktop-layout 同層,搭配 CSS 的 position: fixed 疊加,
  // 才能保證 dialog 蓋住整個桌面版佈局(含側欄)最上層。
  const [settingsOpen, setSettingsOpen] = useState(false)
  // panelMode:rail/side panel 的狀態集中放在 DesktopContent 這一層(而非 rail 或
  // panel 元件內部),因為 rail 的點擊要決定 panel 顯示什麼、panel 收合後也要能
  // 反過來影響 rail 的高亮標記,兩者需共享同一份狀態才不會不同步。
  // 預設值 'channels':進入桌面版時 panel 開啟且顯示頻道列表(維持既有使用者習慣)。
  const [panelMode, setPanelMode] = useState<PanelMode>('channels')
  // timelineMirror:ChatScreen 透過 desktopChat.onTimelineData 鏡像過來的時間軸資料
  // (entries/updatingEntryIDs/taskPlaceholders/refetchEntries)。ChatScreen 是這份
  // 資料唯一的擁有者(它的 WS 連線即時維護這些 state),這裡只是接住鏡像後轉交給
  // side panel 的 MultiTrackTimeline,不可以自己另外 fetch 或開第二條 WS。
  const [timelineMirror, setTimelineMirror] = useState<DesktopTimelineMirror>(EMPTY_TIMELINE_MIRROR)
  const todayRef = useRef<HTMLDivElement>(null as unknown as HTMLDivElement)
  // isSidepanelMode:panelMode 是不是「該展開 side panel」的模式——只有
  // channels/timeline 這兩種正式功能會用到 side panel;demo-cards/demo-row/
  // demo-map 這三種試做模式改成顯示在右側 .desktop-main(取代 ChatScreen,
  // 見下方渲染邏輯),不佔用 side panel,故不能讓 side panel 因為 panelMode
  // 有值就誤判成該展開,否則會出現一個空白的展開面板。
  const isSidepanelMode = panelMode === 'channels' || panelMode === 'timeline'

  // 切換行程時,先清空鏡像資料,避免新行程的 ChatScreen 還沒送出第一次鏡像前,
  // side panel 短暫顯示上一個行程的時間軸內容。
  useEffect(() => {
    setTimelineMirror(EMPTY_TIMELINE_MIRROR)
  }, [activeChannel?.id])

  const onTimelineData = useCallback((data: DesktopTimelineMirror) => {
    setTimelineMirror(data)
  }, [])
  // desktopChat:傳給 ChatScreen 的物件必須記憶化(useMemo),不能直接在 JSX
  // 寫 desktopChat={{ onTimelineData }} 物件字面量——那樣每次 DesktopContent
  // 重新渲染都會建立一個新參照,即使 onTimelineData 本身(已用 useCallback
  // 包過)沒變。ChatScreen 內鏡像時間軸資料的 useEffect 依賴陣列裡有整個
  // desktopChat 物件,參照每次都不同會讓該 effect 每次渲染都重新執行 →
  // 呼叫 onTimelineData → setTimelineMirror → 觸發本元件重新渲染 → 產生新的
  // desktopChat 物件 → 無窮迴圈(實測會直接跳出 React 的
  // "Maximum update depth exceeded" 警告)。用 useMemo 讓這個物件只在
  // onTimelineData 真的變動時才換參照,打斷這個迴圈。
  const desktopChat = useMemo(() => ({ onTimelineData }), [onTimelineData])

  return (
    <>
      <div className="desktop-layout">
        <DesktopRail
          panelMode={panelMode}
          onSelect={(mode) => setPanelMode((cur) => (cur === mode ? null : mode))}
          timelineDisabled={!activeChannel}
          user={props.user}
          isGuest={props.isGuest}
          cfg={cfg}
          onAuthed={props.onAuthed}
          onLogout={props.onLogout}
          onOpenSettings={() => setSettingsOpen(true)}
          isDemo={!!props.isDemo}
        />
        <aside className={`desktop-sidepanel${isSidepanelMode ? '' : ' collapsed'}${panelMode === 'timeline' ? ' wide' : ''}`}>
          <div className="desktop-sidepanel-inner">
            {panelMode === 'channels' && (
              <DesktopChannelList
                cfg={cfg}
                activeChannelID={activeChannel?.id ?? null}
                onOpen={(c) => setActiveChannel(c)}
              />
            )}
            {panelMode === 'timeline' && (
              <div className="desktop-timeline-panel">
                <div className="desktop-sidebar-head">
                  <span className="desktop-sidebar-title">時間軸</span>
                </div>
                <div className="desktop-timeline-scroll">
                  {!activeChannel ? (
                    <div className="empty">選擇一個行程後顯示時間軸。</div>
                  ) : timelineMirror.entries.length === 0 ? (
                    <div className="empty">尚無行程內容。</div>
                  ) : (
                    <MultiTrackTimeline
                      entries={timelineMirror.entries}
                      todayRef={todayRef}
                      updatingIDs={timelineMirror.updatingEntryIDs}
                      taskPlaceholders={timelineMirror.taskPlaceholders}
                      cfg={activeChannel.ownerID === props.user.id ? cfg : undefined}
                      onEntryUpdated={timelineMirror.refetchEntries}
                    />
                  )}
                </div>
              </div>
            )}
          </div>
        </aside>
        <main className="desktop-main">
          {panelMode === 'demo-cards' ? (
            <div className="desktop-demo-panel">
              <div className="desktop-sidebar-head">
                <span className="desktop-sidebar-title">推薦景點卡片(試做)</span>
              </div>
              <div className="desktop-timeline-scroll">
                <RecommendedPlacesList places={FAKE_RECOMMENDED_PLACES} />
              </div>
            </div>
          ) : panelMode === 'demo-row' ? (
            <div className="desktop-demo-panel">
              <div className="desktop-sidebar-head">
                <span className="desktop-sidebar-title">推薦景點橫滑(試做)</span>
              </div>
              <div className="desktop-timeline-scroll">
                <RecommendedPlacesRow places={FAKE_RECOMMENDED_PLACES} />
              </div>
            </div>
          ) : panelMode === 'demo-map' ? (
            <div className="desktop-demo-panel">
              <div className="desktop-sidebar-head">
                <span className="desktop-sidebar-title">推薦景點地圖(試做)</span>
              </div>
              <div className="desktop-timeline-scroll" style={{ padding: 0 }}>
                <RecommendedPlacesMap places={FAKE_RECOMMENDED_PLACES} />
              </div>
            </div>
          ) : activeChannel ? (
            <ChatScreen
              cfg={cfg}
              channel={activeChannel}
              user={props.user}
              onBack={() => setActiveChannel(null)}
              desktopChat={desktopChat}
            />
          ) : (
            <div className="desktop-empty-state">選擇一個行程開始</div>
          )}
        </main>
      </div>
      {settingsOpen && (
        <SettingsDialog
          cfg={cfg}
          user={props.user}
          email={props.email}
          onClose={() => setSettingsOpen(false)}
        />
      )}
    </>
  )
}

// DesktopRail:最左緣 48px 固定寬的 icon rail(比照 VSCode activity bar / Slack
// 頻道列)。上方兩顆圖示鈕切換 side panel 內容(再點一次啟用中的圖示會收合 panel),
// 底部放 DesktopUserMenu。當前啟用的圖示用左緣 accent 豎條 + 底色標記(見 styles.css
// .desktop-rail-btn.active)。
function DesktopRail({
  panelMode,
  onSelect,
  timelineDisabled,
  user,
  isGuest,
  cfg,
  onAuthed,
  onLogout,
  onOpenSettings,
  isDemo,
}: {
  panelMode: PanelMode
  onSelect: (mode: Exclude<PanelMode, null>) => void
  timelineDisabled: boolean
  user: User
  isGuest: boolean
  cfg: ClientConfig
  onAuthed: (token: string, user: User, email: string) => void
  onLogout: () => void
  onOpenSettings: () => void
  // isDemo:網址帶 ?demo 時為 true,才會多渲染下方三顆試做用按鈕
  // (推薦景點卡片/橫滑/地圖,見 RecommendedPlaces.tsx/RecommendedPlacesMap.tsx)。
  // 沒帶 ?demo 時 rail 維持現狀,不多出任何項目。
  isDemo: boolean
}) {
  return (
    <nav className="desktop-rail">
      <div className="desktop-rail-buttons">
        <button
          className={`desktop-rail-btn${panelMode === 'channels' ? ' active' : ''}`}
          onClick={() => onSelect('channels')}
          title="頻道列表"
        >
          <List size={20} strokeWidth={1.8} />
        </button>
        <button
          className={`desktop-rail-btn${panelMode === 'timeline' ? ' active' : ''}`}
          onClick={() => !timelineDisabled && onSelect('timeline')}
          disabled={timelineDisabled}
          title={timelineDisabled ? '請先選擇一個行程' : '時間軸'}
        >
          <Calendar size={20} strokeWidth={1.8} />
        </button>
        {isDemo && (
          <>
            {/* 試做用導覽項目與正式功能之間的視覺分隔線,只在 ?demo 時出現,
                避免試做項目跟正式功能混在一起難以分辨。 */}
            <div className="desktop-rail-divider" />
            <button
              className={`desktop-rail-btn desktop-rail-btn-demo${panelMode === 'demo-cards' ? ' active' : ''}`}
              onClick={() => onSelect('demo-cards')}
              title="推薦景點卡片(試做)"
            >
              <Sparkles size={20} strokeWidth={1.8} />
            </button>
            <button
              className={`desktop-rail-btn desktop-rail-btn-demo${panelMode === 'demo-row' ? ' active' : ''}`}
              onClick={() => onSelect('demo-row')}
              title="推薦景點橫滑(試做)"
            >
              <GalleryHorizontal size={20} strokeWidth={1.8} />
            </button>
            <button
              className={`desktop-rail-btn desktop-rail-btn-demo${panelMode === 'demo-map' ? ' active' : ''}`}
              onClick={() => onSelect('demo-map')}
              title="推薦景點地圖(試做)"
            >
              <Map size={20} strokeWidth={1.8} />
            </button>
          </>
        )}
      </div>
      <DesktopUserMenu
        cfg={cfg}
        user={user}
        isGuest={isGuest}
        onAuthed={onAuthed}
        onLogout={onLogout}
        onOpenSettings={onOpenSettings}
      />
    </nav>
  )
}

// 桌面版側欄頻道列表:複用 useChannelsState(與手機版 ChannelsScreen 共用抓取/建立邏輯),
// 只是呈現方式改成緊湊的側欄列表項目,選中的頻道有高亮(.desktop-channel-item.active)。
function DesktopChannelList({
  cfg,
  activeChannelID,
  onOpen,
}: {
  cfg: ClientConfig
  activeChannelID: string | null
  onOpen: (c: Channel) => void
}) {
  const {
    channels, err, loading,
    creating, setCreating,
    newName, setNewName,
    submitCreate,
  } = useChannelsState(cfg, onOpen)

  return (
    <div className="desktop-channel-list">
      <div className="desktop-sidebar-head">
        <span className="desktop-sidebar-title">行程</span>
        <button className="btn icon-btn" onClick={() => setCreating((v) => !v)} title="新增行程">
          <Plus size={18} strokeWidth={1.8} />
        </button>
      </div>
      {creating && (
        <div className="new-channel-composer">
          <input
            autoFocus
            value={newName}
            placeholder="新行程名稱…"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (isSubmitEnter(e)) submitCreate()
              if (e.key === 'Escape') {
                setCreating(false)
                setNewName('')
              }
            }}
          />
          <button className="btn-primary" onClick={submitCreate} disabled={!newName.trim()}>
            建立
          </button>
        </div>
      )}
      <ErrorBanner msg={err} />
      <div className="desktop-channel-scroll">
        {channels.length === 0 && !err ? (
          <div className="empty">
            {loading ? '載入中…' : '沒有行程，按上方 ＋ 建立一個。'}
          </div>
        ) : (
          channels.map((c) => (
            <button
              key={c.id}
              className={`desktop-channel-item${c.id === activeChannelID ? ' active' : ''}`}
              onClick={() => onOpen(c)}
            >
              <Avatar user={{ name: c.name, avatarColor: 'var(--color-accent)' }} />
              <div className="grow">
                <div className="name">{c.name}</div>
                <div className="sub">
                  {c.lastMessagePreview ?? '尚無訊息'} · {c.memberCount} 人
                </div>
              </div>
            </button>
          ))
        )}
      </div>
    </div>
  )
}

// 桌面版左下方使用者設定入口:頭像 + 名稱一列,點擊展開 popover 選單。
// 已登入時選單只有「設定」(開啟 SettingsDialog)、「登出」兩項精簡項目;
// 訪客狀態維持原邏輯不變,popover 顯示登入表單(LoginForm)。
function DesktopUserMenu({
  cfg,
  user,
  isGuest,
  onAuthed,
  onLogout,
  onOpenSettings,
}: {
  cfg: ClientConfig
  user: User
  isGuest: boolean
  onAuthed: (token: string, user: User, email: string) => void
  onLogout: () => void
  onOpenSettings: () => void
}) {
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onClickOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [open])

  return (
    <div className="desktop-user-menu" ref={menuRef}>
      {open && (
        <div className="desktop-user-popover">
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
              <LoginForm baseURL={cfg.baseURL} onAuthed={(tok, u, mail) => {
                onAuthed(tok, u, mail)
                setOpen(false)
              }} />
            </>
          ) : (
            <>
              <button
                className="desktop-user-menu-item"
                onClick={() => { setOpen(false); onOpenSettings() }}
              >
                <Settings size={16} strokeWidth={1.8} />
                <span>設定</span>
              </button>
              <button
                className="desktop-user-menu-item"
                onClick={() => { onLogout(); setOpen(false) }}
              >
                <LogOut size={16} strokeWidth={1.8} color="var(--ios-red)" />
                <span style={{ color: 'var(--ios-red)' }}>登出</span>
              </button>
            </>
          )}
        </div>
      )}
      <button className="desktop-user-trigger" onClick={() => setOpen((v) => !v)}>
        <Avatar user={user} />
        <div className="grow">
          <div className="name">{isGuest ? '訪客' : user.name}</div>
          {isGuest && <div className="sub">點擊登入</div>}
        </div>
      </button>
    </div>
  )
}

// LLM 回答語言下拉選單:自訂觸發列 + 選項清單,取代原生 <select>,樣式與互動
// 比照 iOS 風格(觸發列排版沿用 .field input,選項清單沿用 .desktop-user-popover
// 的浮層視覺——卡片背景、圓角、陰影)。SettingsDialog(桌面版)/SettingsScreen
// (手機版)共用同一份實作,只各自傳入目前值與 onChange;兩處容器寬度不同但
// 元件本身以 width: 100% 撐滿父層 .field,不需要為此分開兩份程式碼。
// 點擊外部關閉的實作模式沿用 DesktopUserMenu:useRef 抓容器 + mousedown 監聽
// 判斷點擊處是否在容器內。
const ASSIST_LANG_OPTIONS: { value: AssistLang; label: string }[] = [
  { value: 'zh-TW', label: '繁體中文' },
  { value: 'en', label: '英文' },
]

function LangSelect({
  value,
  onChange,
}: {
  value: AssistLang
  onChange: (v: AssistLang) => void
}) {
  const [open, setOpen] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onClickOutside = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [open])

  const current = ASSIST_LANG_OPTIONS.find((o) => o.value === value)

  return (
    <div className="lang-select" ref={boxRef}>
      <button
        type="button"
        className="lang-select-trigger"
        onClick={() => setOpen((v) => !v)}
      >
        <span>{current?.label ?? value}</span>
        <ChevronDown size={16} strokeWidth={1.8} color="var(--ios-gray)" />
      </button>
      {open && (
        <div className="lang-select-popover">
          {ASSIST_LANG_OPTIONS.map((o) => (
            <button
              type="button"
              key={o.value}
              className="lang-select-option"
              onClick={() => { onChange(o.value); setOpen(false) }}
            >
              <span>{o.label}</span>
              {o.value === value && <Check size={16} strokeWidth={2} color="var(--ios-blue)" />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

// 桌面版「設定」dialog:點選 DesktopUserMenu 的「設定」項目後開啟,置中卡片彈窗,
// 視覺沿用原 RecommendedPlacesModal(已移除)留下的 .rp-modal-backdrop/.rp-modal
// 樣式骨架(見 styles.css),內容則對應手機版 SettingsScreen 扣除「登出」
// (登出已是選單裡的獨立項目)。疊加 .settings-dialog-backdrop 只覆寫 position
// 從 absolute 改為 fixed:.rp-modal-backdrop 原本用 absolute+inset:0 是相對
// 最近的 relative 祖先(.desktop-main)定位,只蓋住右側聊天區;這裡是從
// DesktopContent 頂層渲染,需要蓋住整個桌面版佈局(含左側側欄),且不能被
// .desktop-layout 的 overflow: hidden 裁切,故改用 fixed。
function SettingsDialog({
  cfg,
  user,
  email,
  onClose,
}: {
  cfg: ClientConfig
  user: User
  email: string
  onClose: () => void
}) {
  const [health, setHealth] = useState<string>('未測試')
  const [assistLang, setAssistLang] = useState<AssistLang>(() => getAssistLang())
  const [devOpen, setDevOpen] = useState(false)

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
    <div className="rp-modal-backdrop settings-dialog-backdrop" onClick={onClose}>
      <div className="rp-modal settings-dialog" onClick={(e) => e.stopPropagation()}>
        <div className="rp-modal-head">
          <span className="rp-modal-title">設定</span>
          <button className="btn icon-btn" onClick={onClose} title="關閉">
            <X size={18} strokeWidth={1.8} />
          </button>
        </div>
        <div className="rp-modal-body">
          <div className="section-title">目前登入</div>
          <div className="row">
            <Avatar user={user} />
            <div className="grow">
              <div className="name">{user.name}</div>
              <div className="sub">{email || user.id}</div>
            </div>
          </div>
          <div className="section-title">LLM 回答語言</div>
          <div className="field">
            <label>助理回答(assist/語意查詢)使用的語言,不影響介面文字</label>
            <LangSelect
              value={assistLang}
              onChange={(v) => {
                setAssistLang(v)
                localStorage.setItem(ASSIST_LANG_KEY, v)
              }}
            />
          </div>
          <div className="dev-section-toggle" onClick={() => setDevOpen((o) => !o)}>
            <span>開發</span>
            <ChevronDown
              size={16}
              strokeWidth={1.8}
              color="var(--ios-gray)"
              className={devOpen ? 'dev-section-chevron open' : 'dev-section-chevron'}
            />
          </div>
          {devOpen && (
            <>
              <div className="section-title">API Token (CLI 用)</div>
              <TokenDisplay token={cfg.token} />
              <div className="section-title">後端連線</div>
              <div className="field">
                <label>Base URL(由 VITE_API_BASE 設定,不可於此修改)</label>
                <input value={cfg.baseURL} readOnly disabled />
              </div>
              <div className="section-title">健康檢查</div>
              <div className="row" onClick={ping}>
                <div className="grow">
                  <div className="name">GET /health</div>
                  <div className="sub">{health}</div>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}


// ---- 共用小元件 ----

export function Avatar({ user }: { user: { name: string; avatarColor: string } }) {
  const hasColor = !!user.avatarColor
  return (
    <div
      className={hasColor ? 'avatar' : 'avatar avatar-empty'}
      style={hasColor ? { background: user.avatarColor } : undefined}
    >
      {user.name.slice(0, 1)}
    </div>
  )
}

export function ErrorBanner({ msg }: { msg: string | null }) {
  if (!msg) return null
  return <div className="banner"><AlertCircle size={14} strokeWidth={2} style={{verticalAlign: 'middle', marginRight: 6}} />{msg}</div>
}

// 統一把 API 錯誤轉成可顯示訊息。
export function errMsg(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

// Enter 送出,但略過輸入法(注音/中日韓)組字中的 Enter——
// 組字選字時的 Enter 是「確認選字」,不該觸發送出。
export function isSubmitEnter(e: ReactKeyboardEvent): boolean {
  // isComposing:組字進行中。keyCode 229:IME 處理中的按鍵。
  return e.key === 'Enter' && !e.nativeEvent.isComposing && e.keyCode !== 229
}

// ---- 頻道列表:共用資料邏輯(抓取/建立/自動導向預設頻道) ----
// 手機版 ChannelsScreen(整頁列表)與桌面版側欄列表共用同一份 state 管理與 API 呼叫,
// 只有呈現方式(渲染 JSX)不同,避免整套重寫一份。
function useChannelsState(cfg: ClientConfig, onOpen: (c: Channel) => void) {
  const [channels, setChannels] = useState<Channel[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const hasAutoNavigatedRef = useRef(false)

  const load = useCallback(async () => {
    setLoading(true)
    setErr(null)
    hasAutoNavigatedRef.current = false
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

  useEffect(() => {
    if (channels.length > 0 && !hasAutoNavigatedRef.current) {
      const defaultID = localStorage.getItem(LS_DEFAULT_CHANNEL)
      if (defaultID) {
        const defaultChannel = channels.find((c) => c.id === defaultID)
        if (defaultChannel) {
          hasAutoNavigatedRef.current = true
          onOpen(defaultChannel)
        }
      }
    }
  }, [channels, onOpen])

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

  return {
    channels, err, loading,
    creating, setCreating,
    newName, setNewName,
    submitCreate,
  }
}

// ---- 頻道列表頁(手機版:整頁卡片列表) ----

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
  const {
    channels, err, loading,
    creating, setCreating,
    newName, setNewName,
    submitCreate,
  } = useChannelsState(cfg, onOpen)
  const [showLogin, setShowLogin] = useState(false)

  return (
    <>
      <div className="navbar">
        <button className="btn icon-btn" onClick={() => setCreating((v) => !v)}>
          <Plus size={20} strokeWidth={1.8} />
        </button>
        <span className="title">行程</span>
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
        <div className="new-channel-composer">
          <input
            autoFocus
            value={newName}
            placeholder="新行程名稱…"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (isSubmitEnter(e)) submitCreate()
              if (e.key === 'Escape') {
                setCreating(false)
                setNewName('')
              }
            }}
          />
          <button className="btn-primary" onClick={submitCreate} disabled={!newName.trim()}>
            建立
          </button>
        </div>
      )}
      <div className="screen-body">
        <ErrorBanner msg={err} />
        {channels.length === 0 && !err ? (
          <div className="empty">
            {loading ? '載入中…' : '沒有行程。按左上 ＋ 建立一個。'}
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

// ---- 公開分享頁（/public/{token}，無需登入） ----

function PublicViewScreen({ token }: { token: string }) {
  const [data, setData] = useState<{ channelID: string; channelName: string; editable: boolean; entries: Entry[] } | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const todayRef = useRef<HTMLDivElement>(null as unknown as HTMLDivElement)
  const bodyRef = useRef<HTMLDivElement>(null)

  const resolvedBase = BASE_URL

  const reload = () =>
    api.fetchPublicView(resolvedBase, token).then(setData).catch((e) => setErr(errMsg(e)))

  useEffect(() => {
    api.fetchPublicView(resolvedBase, token)
      .then(setData)
      .catch((e) => setErr(errMsg(e)))
      .finally(() => setLoading(false))
  }, [resolvedBase, token])

  useEffect(() => {
    if (data?.channelName) document.title = data.channelName
    return () => { document.title = 'Tripace · 後端測試台' }
  }, [data?.channelName])

  useEffect(() => {
    if (data && todayRef.current && bodyRef.current) {
      bodyRef.current.scrollTo({ top: todayRef.current.offsetTop - 60, behavior: 'instant' })
    }
  }, [data])

  const send = async () => {
    if (!draft.trim() || sending) return
    setSending(true)
    try {
      await api.publicAssist(resolvedBase, token, draft.trim())
      setDraft('')
      await reload()
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setSending(false)
    }
  }

  return (
    <>
      <div className="navbar">
        <span style={{ width: 36 }} />
        <span className="title">{data?.channelName ?? '行程'}</span>
        <span style={{ width: 36 }} />
      </div>
      <div className="screen-body" ref={bodyRef}>
        {loading && <div className="empty">載入中…</div>}
        {err && <div className="banner"><AlertCircle size={14} strokeWidth={2} style={{ verticalAlign: 'middle', marginRight: 6 }} />{err}</div>}
        {data && (
          data.entries.length === 0
            ? <div className="empty">此行程尚無內容。</div>
            : <MultiTrackTimeline entries={data.entries} todayRef={todayRef} />
        )}
      </div>
      {data?.editable && (
        <div className="composer">
          <div className="composer-row">
          <input
            placeholder="新增行程…"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => isSubmitEnter(e) && !e.shiftKey && send()}
            disabled={sending}
          />
          <button onClick={send} disabled={sending || !draft.trim()}>
            <Send size={16} strokeWidth={2} />
          </button>
          </div>
        </div>
      )}
    </>
  )
}

// ---- 設定頁(連線設定 + 測試 health) ----

function SettingsScreen({
  cfg,
  user,
  email,
  isGuest,
  onAuthed,
  onLogout,
  onBack,
}: {
  cfg: ClientConfig
  user: User
  email: string
  isGuest: boolean
  onAuthed: (token: string, user: User, email: string) => void
  onLogout: () => void
  onBack?: () => void
}) {
  const [health, setHealth] = useState<string>('未測試')
  const [assistLang, setAssistLang] = useState<AssistLang>(() => getAssistLang())

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
            <LoginForm baseURL={cfg.baseURL} onAuthed={onAuthed} />
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
          <label>Base URL(由 VITE_API_BASE 設定,不可於此修改)</label>
          <input value={cfg.baseURL} readOnly disabled />
        </div>
        <div className="section-title">LLM 回答語言</div>
        <div className="field">
          <label>助理回答(assist/語意查詢)使用的語言,不影響介面文字</label>
          <LangSelect
            value={assistLang}
            onChange={(v) => {
              setAssistLang(v)
              localStorage.setItem(ASSIST_LANG_KEY, v)
            }}
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
          開發用後端測試台。登入身分存於 localStorage,跨分頁共用同一身分。
          右側 debug panel 記錄每次 API 交易。
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
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
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
      <div className="login-form-actions">
        <button
          className="btn-primary"
          onClick={submit}
          disabled={busy || !email.trim() || !password}
        >
          {busy ? '處理中…' : mode === 'login' ? '登入' : '註冊並登入'}
        </button>
        <div className="login-form-switch">
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
        註冊或登入即表示你同意:僅將本服務用於個人行程規劃與測試,
        不得上傳他人隱私或違法內容;服務資料可能因開發調整而變動或清除,
        請勿作為唯一備份來源。
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
