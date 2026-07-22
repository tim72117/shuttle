import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronLeft, Users, Send, Share2, Sparkles } from 'lucide-react'
import type { ClientConfig } from './api'
import * as api from './api'
import type { Channel, Entry, User } from './types'
import {
  listAllTripBatches,
  listMessageRecommendedPlaces,
  listMessages,
  listMessageTripListKeys,
  replaceTripBatch,
  saveMessage,
  saveMessageRecommendedPlaces,
  saveMessageTripListKeys,
} from './deviceDB'
import { ErrorBanner, errMsg, isSubmitEnter } from './App'
import { MultiTrackTimeline, type TaskPlaceholder } from './Timeline'
import { ClientToolsBridge } from './clienttools/bridge/ClientToolsBridge'
import { defaultClientTools } from './clienttools/tools'
import type { TripBatches, TripEntry } from './clienttools/tripEntryTools'
import { MembersScreen } from './channel/MembersScreen'
import { ShareModal } from './channel/ShareModal'
import { ChannelMenu } from './channel/ChannelMenu'
import { ASSISTANT_ID, ENTRY_QUERY_BATCH_KEY, type ChatMessage } from './chatTypes'
import { AskUserSheet, AskChoiceSheet, type AskChoiceOption } from './AskSheets'
import { MessageBubble } from './MessageBubble'

// mergeTripEntriesById 把 incoming 依 id 合併進 base:id 已存在於 base 就用
// incoming 該筆覆寫(更新),id 不存在就附加到尾端(新增);base 裡這次
// incoming 沒有的其他項目原樣保留、不受影響。用於 entry_query 查詢結果
// (entries_loaded 事件)合併進 ENTRY_QUERY_BATCH_KEY 這個批次——查詢範圍外、
// 使用者可能已在前端編輯過但尚未存回後端的項目不能被這次查詢結果蓋掉。
function mergeTripEntriesById(base: TripEntry[], incoming: TripEntry[]): TripEntry[] {
  const next = base.map((e) => {
    const updated = incoming.find((i) => i.id === e.id)
    return updated ?? e
  })
  const baseIds = new Set(base.map((e) => e.id))
  const additions = incoming.filter((i) => !baseIds.has(i.id))
  return [...next, ...additions]
}

// changedBatchKeys 比較 before/after 兩份 TripBatches 快照,回傳「內容真的
// 不同」的 key 清單(不只是物件參照不同——同一個 key 若剛好被覆寫成內容相同
// 的新陣列,不需要視為變化)。用於 send() 判斷這一輪工具呼叫具體動到了哪些
// 批次(見下方 send() 的 tripListBefore 比對邏輯),取代先前「只知道有沒有
// 變化」的單一參照比對。用 JSON.stringify 比較內容而非逐欄位比對——
// TripEntry 欄位單純(全字串),序列化後直接比字串足夠準確,也不需要為此
// 另外寫一個逐筆 deep-equal helper。
function changedBatchKeys(before: TripBatches, after: TripBatches): string[] {
  const keys = new Set([...Object.keys(before), ...Object.keys(after)])
  const changed: string[] = []
  for (const key of keys) {
    const b = before[key]
    const a = after[key]
    if (b !== a && JSON.stringify(b ?? []) !== JSON.stringify(a ?? [])) {
      changed.push(key)
    }
  }
  return changed
}

// DesktopTimelineMirror:桌面版時間軸所需的資料快照,由 ChatScreen 透過
// desktopChat.onTimelineData 鏡像給外層 DesktopContent(見下方 useEffect)。
// refetchEntries 讓 side panel 裡的 MultiTrackTimeline 在手動編輯(onEntryUpdated)
// 後能觸發 ChatScreen 內部重抓,不必讓 panel 自己另開一份資料來源。
export type DesktopTimelineMirror = {
  entries: Entry[]
  updatingEntryIDs: Set<string>
  taskPlaceholders: TaskPlaceholder[]
  refetchEntries: () => void
}

// desktopChat:非 undefined 時代表目前在桌面模式(由 DesktopContent 傳入)——
// 主區不渲染時間軸、改把時間軸資料透過 onTimelineData 鏡像給外層 side panel。
// 手機路徑完全不傳這個 prop,行為與改版前一致。
export interface DesktopChatOptions {
  onTimelineData: (data: DesktopTimelineMirror) => void
}

export function ChatScreen({
  cfg,
  channel,
  user,
  onBack,
  desktopChat,
}: {
  cfg: ClientConfig
  channel: Channel
  user: User
  onBack: () => void
  desktopChat?: DesktopChatOptions
}) {
  // owner 輸入=發訊息;成員輸入=語意查詢(回答顯示在訊息流,對齊 iOS App)。
  const isOwner = channel.ownerID === user.id
  const [messages, setMessages] = useState<ChatMessage[]>([])
  // latestAnswerID:這次「即時產生」(send() 內)的最新一則答案訊息 id,供
  // MessageBubble 判斷附加資料區塊(推薦景點/旅程清單)要不要預設展開——
  // 只有剛產生的這一則自動展開,其餘一律收合成按鈕(含歷史訊息:load() 從
  // 後端/裝置端 DB 讀回歷史時不設定這個值,故重新整理頁面後永遠是 null,
  // 所有訊息都收合)。只認訊息 id,不用「是不是陣列最後一則」判斷,因為
  // record_entry 的記事泡泡會在同一輪之後被 drop() 移除,順序不足以可靠
  // 代表「最新」。
  const [latestAnswerID, setLatestAnswerID] = useState<string | null>(null)
  // Entry:LLM(record_entry 工具)從訊息解析出的條目,按 messageID 掛到對應訊息下方。
  const [entries, setEntries] = useState<Entry[]>([])
  // updatingEntryIDs:目前正在被 entry_update 工具更新的條目 ID,對應卡片顯示「更新中」光影動畫。
  // WS 收到 entry_updating 加入(並保證最短顯示 800ms),entries_updated(更新完成刷新)時清空。
  const [updatingEntryIDs, setUpdatingEntryIDs] = useState<Set<string>>(new Set())
  const [draft, setDraft] = useState('')
  const [inputFocused, setInputFocused] = useState(false)
  // loaded:這個頻道的 load() 是否已完成過一次(見 load() 的 finally)。
  const [loaded, setLoaded] = useState(false)
  // ask_user:agent 缺資訊(如住宿退房日)時,透過 WS 推來的請求;非 null 時前端開對應 UI。
  const [askUser, setAskUser] = useState<{ askType: string; prompt: string } | null>(null)
  // ask_choice:agent 需要使用者從多個選項擇一時(如多個房型),透過 WS 推來的請求;
  // 非 null 時前端開選單 UI(AskChoiceSheet)。與 ask_user 是獨立的請求/元件,互不相關。
  const [askChoice, setAskChoice] = useState<{ prompt: string; options: AskChoiceOption[] } | null>(null)
  // taskPlaceholders:task_plan 建立中的任務,依 date 在時間軸插入佔位卡;
  // 對應 entry_add(帶 taskID)完成後由 task_entry_ready 移除。
  const [taskPlaceholders, setTaskPlaceholders] = useState<TaskPlaceholder[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  // clienttools 技術可行性驗證:第二條獨立連線(/internal/clienttools/ws),
  // 讓正式對話的 assistant LLM 改呼叫 trip_entry_add/trip_entry_update
  // (取代原本直接寫 Postgres 的 entry_add/entry_update,見
  // server/internal/llm/assistant_agent.go)時,有個瀏覽器分頁能實際執行、
  // 看到結果。這份清單(clientToolsBatches)是全新、獨立的一份前端記憶體
  // 資料——完全不是上面的 entries state,依 ClientToolsBridge 既有的
  // ToolContext 設計(同 ClientToolsDemo.tsx 的既有試做頁面)。這次刻意不要求
  // 把它渲染進時間軸(MultiTrackTimeline)——時間軸的渲染邏輯與資料來源
  // (entries state)完全不受這條連線影響,見下方 clientToolsSessionId 傳給
  // api.assist 之外,沒有任何程式碼路徑讓這條連線碰到
  // entries/updatingEntryIDs/taskPlaceholders。
  //
  // 多批次(key)支援:旅程清單不再是單一一份,而是可能同時存在多批獨立清單
  // (見 web/src/clienttools/tripEntryTools.ts 的 TripBatches 型別、
  // server/tools/clienttools.yaml「多批次(key)支援」)。clientToolsBatches
  // 用一般物件(Record<key, TripEntry[]>)而非 Map:React state 用 Map 時每次
  // 更新都要手動 new Map(prev) 再逐一搬值處理 immutability,一般物件配合展開
  // 運算子({ ...prev, [key]: next })寫法更直接,且這裡不需要 Map 的鍵排序
  // 保證或非字串鍵等特性。
  //
  // 持久化:比照推薦景點(recommendedPlaces)的模式,web/src/deviceDB.ts 的
  // trip_batches 表(schema 有 key 欄位,見該處宣告的說明)能忠實表示多批次
  // 資料——load() 用 listAllTripBatches(channel.id) 一次撈回整個頻道所有
  // 批次當初始值(含 ENTRY_QUERY_BATCH_KEY),send() 偵測到某些 key 的內容
  // 有變化時逐一呼叫 replaceTripBatch 落地(見下方 send() 的 changedKeys
  // 處理)。重新整理頁面後,LLM 透過 trip_entry_add/update 建立的批次資料
  // 現在能正確還原,不再只靠 WS 重連後 ClientToolsBridge 補資料。
  //
  // clientToolsSessionId:連線 ack 後拿到的 sessionId,send() 呼叫 api.assist
  // 時一併帶上,讓後端 trip_entry_* 工具能找到這條連線並轉發呼叫執行(見
  // server/internal/llm/want_analyzer.go Assist 的 SetSessionEnvs 說明)。
  // 只用 ref(不用 state):真正驅動行為的只有 send() 讀的
  // clientToolsSessionIdRef.current(需要「呼叫當下最新值」,見下方 send()
  // 裡的說明);連線狀態(status)/sessionId 曾經給一個常駐面板顯示用,該面板
  // 已移除(旅程清單改掛訊息下方,見 MessageBubble 的 TripListTable),兩個
  // state 因此完全沒有讀取點——不要用 data-* 屬性等方式硬留著只為了消除
  // noUnusedLocals,沒有用途就直接刪,需要時再加回來。
  const [clientToolsBatches, setClientToolsBatches] = useState<TripBatches>({})
  const clientToolsSessionIdRef = useRef<string | null>(null)
  // clientToolsBatchesRef:clientToolsBatches 的唯一真相來源(單一資料,不是
  // state 的鏡射快取)。ClientToolsBridge 建構子現在直接收 getAllBatches/
  // setAllBatches 兩個函式(見下方建立 bridge 處),讀寫都指向這個 ref——
  // bridge 內部不再自己持有 allBatches 副本(先前的設計是 bridge 自存一份、
  // 透過 onEntriesChange 回呼通知外部,但 load() 讀裝置端 DB 還原、
  // entries_loaded WS 事件、TripListTable 刪除按鈕這幾條路徑都是直接改
  // ChatScreen 這裡的 state,不會同步進 bridge 那份獨立副本,曾造成
  // trip_entry_list 查到過期資料的 bug)。
  //
  // 用 setClientToolsBatchesBoth(見下方)這個唯一寫入口統一同步 ref 與
  // state,而非用 useEffect 鏡射 state 到 ref——useEffect 要等 render 完成
  // 才跑,若同一輪(如 api.assist 一次推論中連續呼叫 trip_entry_add 兩次)
  // bridge 連續呼叫 setAllBatches,第二次呼叫時 ref 可能還沒被前一次的
  // useEffect 同步到,读到舊值。ref 由 setClientToolsBatchesBoth 同步且立即
  // 更新,不依賴 React 渲染週期。
  const clientToolsBatchesRef = useRef<TripBatches>({})
  // setClientToolsBatchesBoth:更新 clientToolsBatches 唯一的寫入口,參數
  // 語意比照 React setState(可傳值或 updater 函式),內部同時同步 ref(立即)
  // 與觸發 setClientToolsBatches(讓畫面 re-render)。load()、entries_loaded
  // 事件處理、deleteTripBatchEntries、bridge 的 setAllBatches 全部改用這個
  // 函式,不再直接呼叫 setClientToolsBatches——確保 ref 永遠跟 state 同步,
  // 不會有任何寫入點漏掉更新 ref。
  const setClientToolsBatchesBoth = useCallback(
    (updater: TripBatches | ((prev: TripBatches) => TripBatches)) => {
      const next = typeof updater === 'function' ? updater(clientToolsBatchesRef.current) : updater
      clientToolsBatchesRef.current = next
      setClientToolsBatches(next)
    },
    [],
  )
  // queriedBatchKeysRef：這一輪 send()/api.assist() 期間被 trip_entry_list
  // 查詢過的 key 集合(見 ClientToolsBridge.ts 的 onBatchQueried callback)。
  // trip_entry_list 是純讀取工具,不會改動 clientToolsBatches 的內容,
  // changedBatchKeys 的「內容比對」機制對它完全偵測不到——故用這個獨立的 ref
  // 直接記錄「被查過的 key」,不透過內容比對。用 Set 而非陣列:同一輪可能對
  // 同一個 key 查詢多次(例如分頁查詢同一批),不需要重複記錄。用 ref 而非
  // state:純粹是 send() 這個 async 函式呼叫期間暫存的中介資料,不需要觸發
  // render,同 clientToolsSessionIdRef/clientToolsBatchesRef 的用 ref 慣例。
  // 生命週期:send() 呼叫 api.assist() 前清空,呼叫期間由 ClientToolsBridge
  // 的 onBatchQueried callback 持續累積(trip_entry_list 在 WS session 裡是
  // 阻塞式執行,保證在 api.assist() 的 HTTP 回應返回之前完成,同 tripListBefore
  // 註解處引用的既有時序保證),api.assist() 返回後讀取、跟 changedBatchKeys
  // 算出的 key 合併存進 tripListTriggered,合併後即可讓下一輪 send() 重新清空
  // ——不需要額外清空動作，因為下一輪 send() 開頭就會 clear()。
  const queriedBatchKeysRef = useRef<Set<string>>(new Set())
  // 成員管理在頻道內開啟(對齊 iOS App 的聊天頁右上角入口)。
  const [showMembers, setShowMembers] = useState(false)
  // 分享彈窗
  const [showShare, setShowShare] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)
  const navbarRef = useRef<HTMLDivElement>(null)
  const lastScrollY = useRef(0)
  const todayRef = useRef<HTMLDivElement>(null as unknown as HTMLDivElement)
  // chatOverlayInnerRef:手機版訊息浮層(.chat-overlay-inner)的捲動容器,
  // 條件渲染(只在 messages.length > 0 || sending || inputFocused 時存在),
  // 供進入頻道時「捲到最底」使用(桌面版走 bodyRef,見下方 useEffect)。
  const chatOverlayInnerRef = useRef<HTMLDivElement>(null)

  // 捲動時自動收合/浮現 navbar:只在手機版生效。桌面版 navbar 要持續顯示、
  // 寬度對齊右側顯示區(.desktop-main),不隨捲動收合——桌面版沒有手機那種
  // 「小螢幕捲動時讓出空間」的需求,收合反而會讓 navbar 寬度對齊的視覺
  // 一致性看起來不穩定。desktopChat 非 undefined 即代表目前是桌面模式
  // (見上方 desktopChat prop 的說明)。
  useEffect(() => {
    if (desktopChat) return
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
  }, [!!desktopChat])

  const load = useCallback(async () => {
    setErr(null)
    try {
      // 原話從「裝置端 DB」讀(與 server 隔離);entry/trip 從後端讀(僅 owner)。
      // owner/非 owner 都讀回歷史訊息:owner 的記事原話本來就不會被 saveMessage
      // (記錄了就不留泡泡,見 send() 的 drop(true)),故這裡讀回的只會是曾經
      // 存過的「提問 + 回答」對話紀錄,不影響記事泡泡「記錄了就消失」的既有設計。
      // clientToolsBatches 比照推薦景點的模式持久化(見上方宣告處的說明):
      // 用 listAllTripBatches 一次撈回整個頻道目前存的所有批次(含
      // ENTRY_QUERY_BATCH_KEY 這個 entry_query 專用的固定保留 key,統一走同一套
      // schema/API,見該常數宣告處的說明)當初始值。
      const [msgs, ents, allBatches] = await Promise.all([
        listMessages(channel.id),
        isOwner ? api.fetchEntries(cfg, channel.id) : Promise.resolve([]),
        listAllTripBatches(channel.id),
      ])
      // 推薦景點(recommend_nearby)與旅程清單觸發 key(tripListTriggered)都是
      // 掛在個別訊息底下的附加資料,不在 messages 表裡,故讀完訊息清單後再
      // 各自一次批次撈回這個頻道裡所有訊息對應的資料(而非每則訊息各自查
      // 一次),合併進對應訊息物件。
      const [placesByMsgID, tripListKeysByMsgID] = await Promise.all([
        listMessageRecommendedPlaces(msgs.map((m) => m.id)),
        listMessageTripListKeys(msgs.map((m) => m.id)),
      ])
      const msgsWithExtras: ChatMessage[] = msgs.map((m) => {
        const places = placesByMsgID.get(m.id)
        const tripListTriggered = tripListKeysByMsgID.get(m.id)
        if (!places && !tripListTriggered) return m
        return {
          ...m,
          ...(places ? { recommendedPlaces: places } : {}),
          ...(tripListTriggered ? { tripListTriggered } : {}),
        }
      })
      setMessages(msgsWithExtras)
      setEntries(ents)
      setClientToolsBatchesBoth(allBatches)
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      // loaded:標記這個頻道的初次載入已完成(不論成功或失敗),供下方
      // 「進入頻道時捲到最底」的 effect 判斷時機——特意用獨立旗標而非直接
      // 監聽 messages,因為 messages.length===0(頻道沒有歷史訊息)時無法用
      // 「有沒有內容」區分「初次進入」與「之後使用者送出第一則新訊息」。
      setLoaded(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfg.baseURL, cfg.token, channel.id, isOwner])

  useEffect(() => {
    setLoaded(false)
    load()
  }, [load])

  // 進入頻道(或切換頻道)時捲到最底,讓使用者一進來就看到最新對話,不用
  // 自己往下滑。只監聽 loaded(而非 messages)是關鍵:loaded 從 false 變
  // true 每個頻道週期只發生一次(channel.id 變化時於上方 effect 重置為
  // false、load() 完成時設回 true),天然具備「只觸發一次」的語意——若改
  // 監聽 messages,頻道進來時若沒有歷史訊息(messages.length===0)會無法
  // 區分「初次進入」與「之後使用者送出第一則新訊息」,導致這個 effect 被
  // 誤觸發、跟 scrollMessageToTop(送出訊息時捲到頂端)搶控制權。用
  // requestAnimationFrame 等 loaded 變化觸發的渲染真正完成、DOM(含手機版
  // 條件渲染的 .chat-overlay-inner)已存在,才捲動。
  //
  // 依賴陣列刻意用 !!desktopChat(布林值)而非 desktopChat 本身:desktopChat
  // 是 App.tsx 用內聯物件字面量 {{ onTimelineData }} 傳入的 prop,DesktopContent
  // 每次重新渲染都會建立一個新的物件參照,即使邏輯上未改變。若依賴陣列放
  // desktopChat 物件本身,只要父層重渲染(可能很頻繁),React 就會判定依賴
  // 變了、重跑這個 effect、把使用者剛手動往上拉的位置強制捲回底部——這正是
  // 「訊息沒辦法往上拉、會抖動」的成因。改成布林值後,只有「是否為桌面模式」
  // 這個語意真正改變時才會重新觸發,不受物件參照不穩定影響。
  useEffect(() => {
    if (!loaded) return
    requestAnimationFrame(() => {
      const container = desktopChat ? bodyRef.current : chatOverlayInnerRef.current
      if (!container) return
      container.scrollTo({ top: container.scrollHeight, behavior: 'instant' })
    })
  }, [loaded, !!desktopChat])

  // 桌面模式:把時間軸所需的 state(entries/updatingEntryIDs/taskPlaceholders)
  // 鏡像給外層 DesktopContent 的 side panel,讓 panel 的 MultiTrackTimeline 與
  // 主區共用同一份資料,不必自己另開 WS 或另外 fetch。refetchEntries 供 panel
  // 手動編輯(onEntryUpdated)後觸發重抓——直接複用下面 fetchEntries 的邏輯。
  // 用 useEffect(而非在 render 期間呼叫)是因為 render 期間呼叫外層 setState
  // 會觸發 React 警告(cannot update a component while rendering a different component)。
  useEffect(() => {
    if (!desktopChat) return
    desktopChat.onTimelineData({
      entries,
      updatingEntryIDs,
      taskPlaceholders,
      refetchEntries: () => api.fetchEntries(cfg, channel.id).then(setEntries).catch(() => {}),
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [desktopChat, entries, updatingEntryIDs, taskPlaceholders, cfg.baseURL, cfg.token, channel.id])

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
        // 不論下方有沒有對應的處理分支,先記一筆供 debug panel 顯示
        // (見 api.ts emitWsEvent / DebugPanel 的「WS 事件」分頁)。
        api.emitWsEvent(msg)
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
        } else if (msg.event === 'ask_choice' && Array.isArray(msg.options)) {
          // agent 需要使用者從多個選項擇一;開選單 UI(AskChoiceSheet)。
          setAskChoice({
            prompt: msg.prompt ?? '',
            options: msg.options.map((o: Record<string, unknown>) => ({
              title: String(o.title ?? ''),
              description: typeof o.description === 'string' ? o.description : undefined,
            })),
          })
        } else if (msg.event === 'task_created' && typeof msg.taskID === 'number') {
          // task_plan 建立任務:在該日期下插入一張「新增中」佔位卡。
          setTaskPlaceholders((prev) => [...prev, { taskID: msg.taskID, date: msg.date ?? '', text: msg.text ?? '', kind: msg.kind ?? '' }])
        } else if (msg.event === 'task_entry_ready' && typeof msg.taskID === 'number') {
          // entry_add 已完成對應步驟:移除佔位卡,並重抓條目讓正式卡片出現。
          setTaskPlaceholders((prev) => prev.filter((p) => p.taskID !== msg.taskID))
          api.fetchEntries(cfg, channel.id).then(setEntries).catch(() => {})
        } else if (msg.event === 'entries_loaded' && Array.isArray(msg.entries)) {
          // entry_query 查詢完成(見 server/internal/wanttools/entry_query.go
          // 的 NotifyEntriesLoaded):查到的條目轉成 TripEntry,依 id 合併進
          // ENTRY_QUERY_BATCH_KEY 這個固定批次——不是整批覆蓋,id 已存在就用
          // 新資料覆寫(更新),id 不存在就加入(新增);這個批次裡這次沒查到
          // 的其他項目原樣保留(可能是使用者先前已經在前端編輯過、尚未存回
          // 後端的內容,不能被這次查詢結果蓋掉)。entry_query 本身沒有多批次
          // (key)概念(見 ENTRY_QUERY_BATCH_KEY 宣告處的說明),故固定只動
          // 這一個 key,不影響 LLM 透過 trip_entry_* 操作的其他批次。
          const loaded: TripEntry[] = msg.entries.map((e: Record<string, unknown>) => ({
            id: String(e.id ?? ''),
            title: String(e.title ?? ''),
            date: String(e.date ?? ''),
            time: String(e.time ?? ''),
            note: String(e.note ?? ''),
          }))
          const merged = mergeTripEntriesById(clientToolsBatchesRef.current[ENTRY_QUERY_BATCH_KEY] ?? [], loaded)
          setClientToolsBatchesBoth((prev) => ({ ...prev, [ENTRY_QUERY_BATCH_KEY]: merged }))
          void replaceTripBatch(channel.id, ENTRY_QUERY_BATCH_KEY, merged).catch(() => {})
        }
      } catch {}
    }
    return () => ws.close()
  }, [cfg.baseURL, cfg.token, channel.id])

  // clienttools 技術可行性驗證專用的第二條連線(見上方 clientToolsBatches 的
  // 說明)。只有 owner 會呼叫 send()/api.assist(見下方),故只在 isOwner 時
  // 建立這條連線,對齊既有第一條 WS 也只在 owner 情境下才有實質作用的前提
  // ——member 走 ask()/semanticQuery,不會用到 sessionId。
  // 這條連線目前沿用 /internal/clienttools/ws(掛在 internalAuth 底下,未帶
  // 使用者身分驗證),是本次任務明確接受的已知安全缺口,留待後續處理。
  useEffect(() => {
    if (!isOwner) return
    const bridge = new ClientToolsBridge(
      defaultClientTools,
      {
        // 連線狀態目前沒有任何 UI 讀取(見上方宣告處的說明),不需要接住。
        onStatusChange: () => {},
        onToolNamesChange: () => {},
        // trip_entry_list(純讀取,不改動 allBatches)透過這個平行、獨立的
        // 通知口子回報「剛查詢了這個 key」（見 ClientToolsBridge.ts
        // onBatchQueried 型別定義處的說明）——累積進 queriedBatchKeysRef，
        // 供 send() 在 api.assist() 返回後跟 changedBatchKeys 算出的 key
        // 合併進 tripListTriggered（見上方 queriedBatchKeysRef 宣告處）。
        onBatchQueried: (key) => {
          queriedBatchKeysRef.current.add(key)
        },
        // assistant_message 是 clienttoolsRole 專屬對話(ClientToolsDemo.tsx
        // 那條路徑)的回覆,這條連線在 ChatScreen 裡只用來讓 trip_entry_*
        // 工具找到執行對象,不會透過這條連線送 prompt,故不需要顯示文字回覆。
        onAssistantText: () => {},
        onLog: () => {},
        onBusyChange: () => {},
        onSessionId: (id) => {
          clientToolsSessionIdRef.current = id
        },
      },
      // getAllBatches/setAllBatches:讀寫都直接指向 clientToolsBatchesRef
      // (透過 setClientToolsBatchesBoth 統一寫入口,見該處宣告的說明)——
      // clientToolsBatchesRef 是唯一真相來源,bridge 不再自己持有副本。
      // trip_entry_* 工具執行時即時讀到的是「呼叫當下最新值」,不論這份資料
      // 是被 load() 從裝置端 DB 還原、entries_loaded WS 事件更新、還是
      // TripListTable 刪除按鈕改的,bridge 都能立刻看到,不會有分歧。
      () => clientToolsBatchesRef.current,
      (next) => setClientToolsBatchesBoth(next),
    )
    bridge.connect()
    return () => {
      bridge.disconnect()
      clientToolsSessionIdRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在頻道/owner 身分變動時重新連線,同第一條 WS 的依賴慣例。
  }, [cfg.baseURL, channel.id, isOwner])

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

  // 桌面版:把指定訊息(依 msgID)捲到可視區域頂端,讓 LLM 的回答有完整空間
  // 往下展開;內容若超出可視高度才繼續往上捲(瀏覽器原生行為接手,這裡只
  // 負責初始定位)。手機版走 .chat-overlay 浮層獨立捲動,是完全不同的 DOM
  // 結構,呼叫端要自行判斷 desktopChat 是否成立才呼叫這個函式。用
  // requestAnimationFrame 等這次觸發的渲染真正完成、DOM 節點已存在,才能
  // 量到正確位置。send()/ask() 共用同一份邏輯。
  const scrollMessageToTop = (msgID: string) => {
    requestAnimationFrame(() => {
      const container = bodyRef.current
      if (!container) return
      // 用 dataset 比對而非把 id 直接插進 querySelector 字串,避免 id 內容
      // 若含特殊字元導致 CSS selector 語法錯誤(目前 id 是純數字時間戳不會
      // 發生,但這樣寫不依賴這個前提)。
      const target = Array.from(container.querySelectorAll<HTMLElement>('[data-msg-id]'))
        .find((el) => el.dataset.msgId === msgID)
      if (target) {
        // 用 getBoundingClientRect 算 target 相對 container 的實際視覺距離,
        // 不用 target.offsetTop——offsetTop 是相對「最近的 offsetParent」
        // (第一個 position 非 static 的祖先),若兩者之間有任何一層元素設了
        // position: relative(不一定是 container 本身),算出來的值會偏小,
        // 導致捲動量不足、只推到中途而非真正的容器頂端。
        const containerRect = container.getBoundingClientRect()
        const targetRect = target.getBoundingClientRect()
        const offset = targetRect.top - containerRect.top + container.scrollTop
        container.scrollTo({ top: offset, behavior: 'smooth' })
      }
    })
  }

  // owner 用:統一輸入送進 assist,LLM 自主判斷記錄事項或回答提問。
  // overrideText:由 ask_user 回填等場景直接指定送出內容(不從 draft 取)。
  const send = async (overrideText?: string) => {
    const text = (overrideText ?? draft).trim()
    if (!text) return
    setSending(true)
    setErr(null)
    if (overrideText === undefined) setDraft('') // 只清使用者手打的草稿;ask_user 回填不動 draft
    // 立刻插入使用者這則泡泡 + 處理中佔位泡泡(海浪動畫):兩者同時 append,
    // 避免使用者的話晚於佔位泡泡才出現、事後插隊到前面的順序問題。
    // record 情境下原話最終不保留(見下方 drop 的呼叫處),但送出當下仍先顯示,
    // 讓使用者能立刻看到自己剛打的內容。
    const askID = `ask_${Date.now()}`
    const askMsg = mkLocalMsg(askID, user.id, user.name, text)
    const pendingID = `pending_${Date.now()}`
    const pending = mkLocalMsg(pendingID, ASSISTANT_ID, '', '')
    pending.pending = true
    setMessages((prev) => [...prev, askMsg, pending])
    // 送出當下就捲(跟 ask() 一致,不等 api.assist 回應)。若最終走記事分支,
    // 泡泡稍後會被 drop(true) 整個移除,使用者會經歷「先捲上去、內容隨即
    // 消失」的短暫跳動——這是已知取捨,換取「送出當下立即有回饋」的一致性。
    if (desktopChat) scrollMessageToTop(askID)
    // drop:移除佔位泡泡。record 情境額外連同使用者原話泡泡一起移除
    // (原話已歸入 entry 卡,訊息流不重複保留);回答情境只移除佔位泡泡,
    // 使用者泡泡保留在原位。
    const drop = (alsoDropAsk = false) =>
      setMessages((prev) => prev.filter((m) => m.id !== pendingID && (!alsoDropAsk || m.id !== askID)))
    // record 時 agent 非同步寫 entry,記下送出前的數量當基準,輪詢到變多才算寫完。
    const baseCount = entries.length
    // tripListBefore:送出前先存一份 clientToolsBatches 的參照(entry_query/
    // trip_entry_add/trip_entry_update 都是在 api.assist() 這輪 LLM 推論「過程中」
    // 透過 sessionID 路由回瀏覽器阻塞式執行——entry_query 的 entries_loaded WS
    // 事件與 trip_entry_* 觸發 ClientToolsBridge 呼叫 setAllBatches 都是在
    // 對應工具的 Call() 回傳給 LLM 之前就已經觸發,而 want orchestrator 對整輪
    // Submit 的等待(w.orch.Submit → <-done)必須等這輪所有工具呼叫完成才會
    // idle,故這些 state 更新保證發生在 api.assist() 的 HTTP 回應返回之前;
    // 詳見 server/internal/llm/want_analyzer.go Assist() 與
    // server/internal/clienttools/interaction.go askPage 的阻塞呼叫鏈)。
    // 沒有後端欄位能直接告知「這輪具體動到了哪些批次(key)」,故用「呼叫前後
    // clientToolsBatches 各 key 的內容是否不同」這個最小成本的前端判斷方式
    // (見上方 changedBatchKeys):用 ref 讀「呼叫當下最新值」（同
    // clientToolsBatchesRef 一貫的用 ref 而非 state 閉包的理由),呼叫完後跟
    // 屆時最新的 clientToolsBatchesRef.current 逐 key 比較,取得具體變化的
    // key 清單而不只是「有沒有變化」的布林值。
    const tripListBefore = clientToolsBatchesRef.current
    // queriedBatchKeysRef 清空供這一輪重新累積——trip_entry_list 若在這輪
    // api.assist() 期間被呼叫,ClientToolsBridge 的 onBatchQueried callback
    // 會把查到的 key 加進來(見上方宣告處與 onBatchQueried 接線處的說明)。
    // 必須在這裡（api.assist() 呼叫之前）清空，不能提前到函式更早處或延後到
    // 呼叫之後——提前會导致上一輪殘留的 key 被這裡清掉前就已經讀走（此處不適用，
    // 因為上一輪早已讀完並存進對應訊息），延後則會把這一輪剛累積到一半的 key
    // 清空、遺漏。
    queriedBatchKeysRef.current.clear()
    try {
      // clientToolsSessionIdRef(而非 state)：send 是個 async 函式，用 ref
      // 讀「呼叫當下最新的連線狀態」，避免閉包捕捉到建立當下（可能連線還沒
      // ack）的舊值——同 sending 等其餘欄位在此函式裡都直接讀 state 的既有
      // 寫法不同，是因為這裡要的是「送出當下的最新值」而非「render 當下」
      // 的值，两者在 WS 非同步 ack 到達的情境下可能不同。
      const res = await api.assist(cfg, channel.id, text, clientToolsSessionIdRef.current ?? undefined)
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
        // 記錄了 → 原話已歸入上方 entry 卡,訊息流不保留這則原話泡泡
        // (連同送出當下先插入的使用者泡泡一起移除)。
        // 對齊 iOS:記事原話存而不顯,內容由 entry 承載。
        drop(true)
      } else {
        // 回答了 → 佔位泡泡就地換成答案(使用者泡泡已在送出當下插入,原位不動)。
        // 答案泡泡掛上 agent 用 present_entries 輸出的條目、recommend_nearby 查到
        // 的候選景點,前端分別用列表元件顯示在該則訊息底下(而非全域彈窗)。
        const ans = mkLocalMsg(`ans_${Date.now()}`, ASSISTANT_ID, '', res.answer)
        ans.presented = res.entries
        ans.recommendedPlaces = res.recommendedPlaces
        // 這輪 clientToolsBatches 若有變化,代表觸發了 entry_query 或
        // trip_entry_add/trip_entry_update(見上方 tripListBefore 的說明)。
        // 記下具體變化的 key 清單(可能不只一個——同一輪若先後對兩個不同批次
        // 操作,如新增到 tokyo_trip 後又更新 osaka_trip 裡的一筆),不只是
        // 布林值。只存 key 清單,不存清單內容快照——MessageBubble 渲染時讀
        // 當下最新的 clientToolsBatches state(props 傳入),不是這裡比對
        // 用的舊值。
        const changedKeys = changedBatchKeys(tripListBefore, clientToolsBatchesRef.current)
        // 跟這一輪被 trip_entry_list 查詢過的 key(queriedBatchKeysRef,見該處
        // 宣告的說明)合併——查詢類工具不改動內容,changedBatchKeys 偵測不到,
        // 需要這條獨立路徑補上。用 Set 去重:同一個 key 若既被查詢過、又被
        // 寫入類工具改過,tripListTriggered 裡只需要出現一次(MessageBubble
        // 依 key 陣列各自 render 一個 TripListTable,重複的 key 會渲染出重複
        // 的表格)。讀完立刻視為這一輪的終態——下一輪 send() 開頭會重新
        // clear() queriedBatchKeysRef,這裡不需要額外清空。
        const triggeredKeys = new Set([...changedKeys, ...queriedBatchKeysRef.current])
        if (triggeredKeys.size > 0) {
          ans.tripListTriggered = [...triggeredKeys]
        }
        setMessages((prev) => prev.map((m) => (m.id === pendingID ? ans : m)))
        // latestAnswerID:標記這則答案是「這次即時產生」的最新一則,供
        // MessageBubble 判斷附加資料區塊(推薦景點/旅程清單)預設展開(見
        // AttachmentsPanel 與上方 latestAnswerID 宣告處的說明)。只有這條
        // 「回答了」分支會產生 presented/recommendedPlaces/tripListTriggered,
        // record 分支(上方 drop(true))沒有附加資料可展開,不需要設定。
        setLatestAnswerID(ans.id)
        // 提問 + 答案存裝置 DB(比照 ask() 的做法),重新整理/切回頻道後仍看得到。
        void saveMessage(askMsg).catch(() => {})
        void saveMessage(ans).catch(() => {})
        // 推薦景點(若非空)一併存進裝置 DB,掛在這則答案訊息底下,讓重新整理
        // /切回頻道後 load() 讀回時仍能還原(見下方 load() 的批次查詢)。
        if (ans.recommendedPlaces && ans.recommendedPlaces.length > 0) {
          void saveMessageRecommendedPlaces(ans.id, ans.recommendedPlaces).catch(() => {})
        }
        // 旅程清單觸發 key(若非空)一併存進裝置 DB,掛在這則答案訊息底下,
        // 讓重新整理/切回頻道後 load() 讀回時仍知道該掛哪些 key 的表格
        // (見上方 tripListTriggered 型別欄位的說明、load() 的批次查詢)。
        if (ans.tripListTriggered && ans.tripListTriggered.length > 0) {
          void saveMessageTripListKeys(ans.id, ans.tripListTriggered).catch(() => {})
        }
        // 這輪真的有變化的批次(key)內容存進裝置 DB(比照推薦景點的持久化
        // 模式)——只存 changedKeys(內容真的不同),不含 queriedBatchKeysRef
        // 裡「只是被查詢過、內容沒變」的 key,避免無意義的整批覆寫。用
        // clientToolsBatchesRef.current[key](呼叫當下最新值,同本函式一貫
        // 用 ref 讀值的理由)而非 tripListBefore。
        for (const key of changedKeys) {
          void replaceTripBatch(channel.id, key, clientToolsBatchesRef.current[key] ?? []).catch(() => {})
        }
      }
    } catch (e) {
      // 失敗:只移除佔位泡泡,使用者泡泡保留(讓使用者仍看得到剛才送出的內容)。
      drop()
      setErr(errMsg(e))
      setDraft(text) // 失敗時還回草稿
    } finally {
      setSending(false)
    }
  }

  // deleteTripBatchEntries:TripListTable 勾選項目後按刪除,把選中的 id 從
  // 對應批次(key)移除。與批次來源無關(LLM 自訂 key 或 ENTRY_QUERY_BATCH_KEY
  // 查詢結果一視同仁,都只是移除 clientToolsBatches 裡這個 key 陣列中的幾筆)
  // ——這裡只動前端記憶體 + 裝置端 DB(比照 replaceTripBatch 的持久化模式),
  // 不呼叫後端 api.deleteTripEntry 刪 Postgres(那是另一個功能範疇,不在這次
  // 需求內)。用 setClientToolsBatchesBoth(而非直接 setClientToolsBatches):
  // 這是唯一真相來源的寫入口,自動同步 clientToolsBatchesRef,bridge 的
  // getAllBatches 讀的就是這個 ref,故刪除後 LLM 之後查詢會立刻讀到最新結果,
  // 不需要額外的同步步驟。
  const deleteTripBatchEntries = (key: string, ids: Set<string>) => {
    setClientToolsBatchesBoth((prev) => {
      const next = (prev[key] ?? []).filter((e) => !ids.has(e.id))
      const updated = { ...prev, [key]: next }
      void replaceTripBatch(channel.id, key, next).catch(() => {})
      return updated
    })
  }

  // 成員用:自然語言查詢頻道。問答持久化進裝置端 DB(重開頻道仍在,後端不存)。
  const ask = async (overrideText?: string) => {
    const q = (overrideText ?? draft).trim()
    if (!q) return
    setSending(true)
    setErr(null)
    if (overrideText === undefined) setDraft('')
    // 提問泡泡(持久化)+ 處理中佔位泡泡(海浪動畫,暫態)。
    const askMsg = mkLocalMsg(`ask_${Date.now()}`, user.id, user.name, q)
    const pendingID = `pending_${Date.now()}`
    const pending = mkLocalMsg(pendingID, ASSISTANT_ID, '', '')
    pending.pending = true
    setMessages((prev) => [...prev, askMsg, pending])
    // 提問存裝置 DB。
    void saveMessage(askMsg).catch(() => {})
    // 一定是查詢情境,送出當下就確定要捲(不像 send() 要等回應才知道是
    // 記事還是回答分支)。
    if (desktopChat) scrollMessageToTop(askMsg.id)
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
        {desktopChat ? (
          // 桌面模式:主區不渲染時間軸(時間軸只活在左側 side panel 的時間軸模式裡)。
          // 不同於手機版的浮層疊層設計(時間軸在底層、對話泡泡浮在上方,兩者各自
          // 獨立捲動)——桌面版沒有時間軸需要被浮層蓋住看見,底層只會是引導文字,
          // 故引導文字與對話泡泡改成同一個 .screen-body 容器內的一般文件流內容,
          // 整個對話區當一個整體捲動,捲軸貼齊右欄邊緣,不再套用 .chat-overlay。
          <div className="screen-body chat-messages" ref={bodyRef} onMouseDown={() => setInputFocused(false)}>
            <ErrorBanner msg={err} />
            {messages.length === 0 ? (
              <div className="empty">
                {isOwner ? '在下方輸入記事，會依時間排列在左側時間軸。' : '在下方查詢這趟行程的內容。'}
              </div>
            ) : (
              messages.map((m) => (
                <MessageBubble key={m.id} msg={m} meID={user.id} tripBatches={clientToolsBatches} isLatest={m.id === latestAnswerID} onDeleteTripBatchEntries={deleteTripBatchEntries} />
              ))
            )}
          </div>
        ) : (
          <>
            <div className="screen-body" ref={bodyRef} onMouseDown={() => { setInputFocused(false); setMessages([]) }}>
              <ErrorBanner msg={err} />
              {entries.length === 0 && messages.length === 0 && !sending ? (
                <div className="empty">
                  {isOwner ? '在下方輸入記事，會依時間排列在這裡。' : '在下方查詢這趟行程的內容。'}
                </div>
              ) : entries.length > 0 ? (
                <MultiTrackTimeline
                  entries={entries}
                  todayRef={todayRef}
                  updatingIDs={updatingEntryIDs}
                  taskPlaceholders={taskPlaceholders}
                  cfg={isOwner ? cfg : undefined}
                  onEntryUpdated={() => api.fetchEntries(cfg, channel.id).then(setEntries).catch(() => {})}
                />
              ) : null}
            </div>

            {/* 浮層：訊息對話區，覆蓋在時間軸上方，毛玻璃背景 */}
            {(messages.length > 0 || sending || inputFocused) && (
              <div className="chat-overlay" onMouseDown={(e) => e.stopPropagation()}>
                <div className="chat-overlay-inner" ref={chatOverlayInnerRef}>
                  {messages.map((m) => (
                    <MessageBubble key={m.id} msg={m} meID={user.id} tripBatches={clientToolsBatches} isLatest={m.id === latestAnswerID} onDeleteTripBatchEntries={deleteTripBatchEntries} />
                  ))}
                </div>
              </div>
            )}
          </>
        )}

        <div className="composer">
          <div className="composer-row">
            <button
              className="composer-fn-btn"
              onClick={() => {
                // 直接送出固定語句,不動 draft:isOwner 記事流程會被情況 D
                // 判定為推薦意圖並呼叫 recommend_nearby;非 owner 走查詢流程,
                // 一樣是問一句話交給 AI 回答(見 assistant_agent.go recommendThought)。
                const q = '推薦附近的景點'
                isOwner ? send(q) : ask(q)
              }}
              disabled={sending}
              title="推薦附近景點"
            >
              <Sparkles size={20} strokeWidth={1.8} />
            </button>
            <input
              autoFocus
              value={draft}
              placeholder={isOwner ? '記事或提問…' : '用自然語言查詢這趟行程…'}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => isSubmitEnter(e) && (isOwner ? send() : ask())}
              onFocus={() => setInputFocused(true)}
              onBlur={() => setInputFocused(false)}
            />
            <button
              onClick={() => (isOwner ? send() : ask())}
              disabled={sending || !draft.trim()}
            >
              <Send size={18} strokeWidth={2} />
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
      {askChoice && (
        <AskChoiceSheet
          prompt={askChoice.prompt}
          options={askChoice.options}
          onCancel={() => setAskChoice(null)}
          onSubmit={(title) => {
            setAskChoice(null)
            // 把選中選項的主標題當成一則新訊息送回,agent 靠對話歷史接上前文(比照 ask_user)。
            send(title)
          }}
        />
      )}
    </>
  )
}
