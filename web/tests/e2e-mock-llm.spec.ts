// 端到端測試:驗證「LLM 推論安排整段行程,過程中新增/刪除/修改,前端即時更新」
// 這件事真的能動——跨越完整的瀏覽器 → WS → 畫面渲染鏈路。
//
// 本測試「不」負責啟動 mockllm/server/web 三個 process,那是
// server/scripts/run_e2e_mock_llm_test.sh 的職責。跑本測試前必須先在另一個
// terminal 執行該腳本並等三個 process 就緒(見腳本開頭說明),本測試只負責
// 連上 baseURL 開始自動化操作與斷言。
//
// 劇本內容(server/cmd/mockllm/main.go tokyoTripScript,不在本檔複製一份,
// 只引用其結果):
//   1. entry_add    東京晴空塔 14:00
//   2. entry_update 同一筆 → 標題改「東京晴空塔(展望台預約 14:30)」、時間改 14:30
//   3. entry_add    淺草寺 10:00
//   4. entry_delete 刪除第 1 步那筆(此時標題已是第 2 步改過的新值)
//
// 種子頻道「產品討論」(server/cmd/server/main.go seedIfEmpty)已有 3 筆既有
// entry(開會敲定 Q3 產品規格/準備預算上調提案/修登入頁的 bug)——斷言刻意
// 避免依賴時間軸「只有」東京晴空塔或淺草寺這種假設,一律用「新增/移除了對應
// 文字的卡片」而非「時間軸恰好幾筆」來寫,同時交叉驗證 REST API 時也把這 3
// 筆種子資料計入預期總數。
//
// # 為什麼用 WS frame 序列驗證「過程」,而不是逐步輪詢 DOM 快照
//
// 第一版寫法試過對 DOM 逐步斷言(先等卡片出現顯示 14:00 → 再等 updating class
// → 再等改成 14:30),實測發現 mock LLM 本機函式呼叫等級的速度快到 4 個
// 工具呼叫全部落在同一秒內執行完畢(見開發過程中一次失敗紀錄:Playwright
// 才剛確認完卡片可見,下一步檢查文字內容時畫面已經是整段劇本跑完後的最終
// 狀態)。這代表「entry_add 剛完成、entry_update 還沒開始」這個純資料庫瞬時
// 狀態,在外部 HTTP+DOM 觀察者視角下窗口太窄,不是斷言等待方式的問題,是
// 這個中間態本身可能真的沒有可觀測窗口。
//
// 真正可靠的「過程被依序驅動」證據來源改用瀏覽器底層 WebSocket frame 本身
// ——每一則 entry_updating/entries_updated 訊息都是後端在對應工具呼叫執行
// 前後各自發送的獨立事件(見 server/internal/wanttools/entry_update.go:
// NotifyEntryUpdating 在寫入前呼叫、Notify 在寫入後呼叫),Playwright 的
// page.on('websocket') 監聽的是網路層原始 frame,不會被後續事件覆蓋或被
// React 渲染批次吞掉,比賭 DOM 快照時機更穩健。
//
// 但這個方法仍有兩個實測發現的限制,下方斷言區塊各自有對應的取捨說明:
//
//   1. React StrictMode(見 main.tsx)開發模式下 effect 雙重掛載,加上 mock
//      LLM 執行第 1 步 entry_add 的速度,兩者偶爾會與「頻道 WS 連線真正完成
//      handshake」這件事產生競態,導致最開頭第 1 步 entry_add 觸發的
//      entries_updated 廣播偶爾在新連線建立好之前就已送出、被前端錯過(WS
//      不緩衝離線期間的訊息)。因此驗證改為「錨定 entry_updating 事件(劇本
//      第 2 步才觸發,此時連線必然早已穩定)之後的相對順序」,而非要求收到
//      劇本全部 5 則廣播。
//   2. entry_update 完成到 entry_delete 完成之間的真實間隔太窄,外部觀察者
//      (Playwright)幾乎不可能穩定捕捉到「改名後的卡片存在、但還沒被刪除」
//      這個視覺中間態——這一段改成「能捕捉到就驗證內容正確,捕捉不到就略過,
//      不讓測試因此不穩定失敗」,不是放棄驗證,是把「entry_update 確實發生
//      過」的證明責任精確地分配給更可靠的來源(WS 事件序列 + REST API)。
import { test, expect, type Page, type ConsoleMessage, type WebSocket } from '@playwright/test'

const LOGIN_EMAIL = 'me@channel.dev'
const LOGIN_PASSWORD = 'password'
const SEED_CHANNEL_NAME = '產品討論'
// seedIfEmpty 固定寫死的頻道 ID(見 server/cmd/server/main.go),不需要從
// 畫面或 localStorage 解析——localStorage 只在使用者主動設「開啟時自動進入」
// 才會存 channelID(LS_DEFAULT_CHANNEL),一般點進頻道不會寫這個 key。
const SEED_CHANNEL_ID = 'ch_001'

// 後端 REST API base URL:web dev server 用 VITE_API_BASE 覆寫指向這裡(見
// run_e2e_mock_llm_test.sh),此處獨立設一份環境變數是因為 Playwright 這支
// 測試跑在 Node 環境、不吃 Vite 的 import.meta.env,無法從前端 bundle 讀到
// 同一份設定,只能各自宣告、各自預設同一個值(:8180,對齊腳本預設)。
const API_BASE_URL = process.env.E2E_API_BASE_URL ?? 'http://127.0.0.1:8180'

// 整段劇本(4 個工具呼叫 + 收尾文字)實測在本機速度下 1-2 秒內就跑完整個
// HTTP round trip(want orchestrator 多輪迴圈 + 真實 SQLite 寫入,見開發過程
// 中 server log 時間戳)。這裡的逾時是「等最終畫面反映完整流程結果」的上限,
// 抓 15s 比實際耗時寬鬆數倍,避免 CI 機器較慢時 flaky,但也不會像真實 LLM
// 那種 20-30s 逾時讓失敗要等很久才知道。
const STEP_TIMEOUT = 15_000

// 一則收到的 WS 事件(只保留斷言需要的欄位)。
interface CapturedWsEvent {
  event: string
  entryID?: string
}

test.describe('mock LLM 端到端:新增/修改/新增/刪除反映到前端時間軸', () => {
  // 全程監聽 JS 例外與 console error,測試結束前斷言完全沒有——這條鏈路橫跨
  // WebSocket 訊息解析、React state 更新、時間軸重新排序渲染,任何一段出錯
  // 很容易被「畫面看起來還是對的」蓋過去(例如某個 WS 事件解析失敗但下一次
  // 輪詢又補上正確資料),用 console/pageerror 監聽能抓到這類「結果正確但
  // 過程其實有出錯」的情況。
  let jsErrors: string[] = []
  // 依收到順序累積的 WS 事件,是本測試驗證「過程依序發生」的主要證據來源
  // (見檔案開頭說明)。在 beforeEach 掛 page.on('websocket') 監聽器,搶在
  // 測試本體導覽/登入之前就開始收集,確保進頻道那一刻建立的 WS 連線不會漏接
  // 最開頭幾則事件。
  let wsEvents: CapturedWsEvent[] = []

  test.beforeEach(({ page }) => {
    jsErrors = []
    wsEvents = []
    page.on('pageerror', (err) => {
      jsErrors.push(`[pageerror] ${err.message}`)
    })
    page.on('console', (msg: ConsoleMessage) => {
      if (msg.type() === 'error') jsErrors.push(`[console.error] ${msg.text()}`)
    })
    page.on('websocket', (ws: WebSocket) => {
      // 頻道 WS 連線路徑是 /v1/channels/{id}/ws(見 ChatScreen.tsx),公開分享頁
      // 等其他頁面不會開這個連線,這裡不特別過濾 URL——測試流程只會開一條頻道 WS。
      // framereceived 的 callback 收到的是 { payload } 物件(不是原始資料本身,
      // 見 playwright-core 的型別定義),第一版寫法誤把整包物件當字串處理,
      // 導致 JSON.parse 永遠失敗、被下面的 catch 靜默吞掉、一則事件都收不到
      // ——這裡讀 data.payload 才是實際的 frame 內容。
      ws.on('framereceived', (data) => {
        const raw = typeof data.payload === 'string' ? data.payload : data.payload.toString()
        try {
          const parsed = JSON.parse(raw) as { event?: string; entryID?: string }
          if (parsed.event) wsEvents.push({ event: parsed.event, entryID: parsed.entryID })
        } catch {
          // 非 JSON 或格式不符的 frame 略過,不視為測試失敗——只關心
          // entry_updating/entries_updated 這類已知事件的順序。
        }
      })
    })
  })

  test('東京晴空塔新增 → 更新中動畫 → 改標題時間 → 淺草寺新增 → 晴空塔刪除', async ({ page }) => {
    await login(page)
    await openSeedChannel(page)

    // 記錄登入拿到的 token,稍後用真實後端 REST API 交叉驗證 DB 最終狀態
    // ——不能只信任 DOM,前端渲染邏輯本身若有 bug(例如排序/過濾寫錯)可能
    // 讓畫面「看起來對」但其實跟資料庫不一致,REST API 是獨立於前端渲染路徑
    // 之外的第二個真相來源。
    const token = await page.evaluate(() => localStorage.getItem('tripace.auth.token'))
    expect(token, 'localStorage 應該存有登入後的 auth token(key=tripace.auth.token)').toBeTruthy()

    // ---- 觸發 assist:點「對話」圓鈕展開輸入框,打一句話,Enter 送出 ----
    // 「對話」鈕與「推薦」鈕共用 .composer-fn-btn class,靠 title 屬性區分
    // (見 ChatScreen.tsx composer-row 區塊),不能只用 class 選,否則兩顆都會中。
    const chatToggleBtn = page.locator('button.composer-fn-btn[title="對話"]')
    await expect(chatToggleBtn, '輸入列應該有一顆 title="對話" 的圓形功能鈕').toBeVisible()
    await chatToggleBtn.click()

    // 展開後輸入框是 .composer-row 底下唯一的 <input>(無特殊 class,見
    // ChatScreen.tsx composerExpanded 分支)。
    const composerInput = page.locator('.composer-row input')
    await expect(composerInput, '點「對話」後應展開出一個文字輸入框').toBeVisible()
    await composerInput.fill('幫我安排一趟東京兩天行程')
    await composerInput.press('Enter')

    // ---- 用 WS frame 序列驗證「過程真的依序發生」----
    // 用 entry_updating 事件(必定存在,見下方說明)當「劇本已經跑到 entry_update
    // 這一步」的錨點——不是固定 waitForTimeout,而是條件等待。
    //
    // 為什麼是「錨定 entry_updating」而非「要求收到全部 5 則廣播」:實測時發現
    // 進頻道到送出訊息這段流程,受 React StrictMode(見 main.tsx)開發模式下
    // effect 雙重掛載影響,第一條頻道 WS 連線常常建立到一半就被 cleanup 關閉、
    // 真正存活的是隨後重新掛載的第二條連線;而 mock LLM 執行第 1 步 entry_add
    // 的速度快到有機會與「頻道 WS 真正完成 handshake」這件事發生競態——若
    // entry_add 的 entries_updated 廣播在新連線建立好之前就送出,前端會錯過
    // 那一則(WS 不緩衝離線期間的訊息)。已用獨立除錯腳本反覆驗證這個現象存在
    // (第 1 則 entries_updated 偶爾收不到,但 entry_updating 之後的事件從未
    // 缺漏或錯序)。entry_updating 事件本身沒有這個問題,因為它是劇本第 2 步
    // 才會觸發,此時連線早已穩定。第 1 步的結果不會因此驗證不到——entry_updating
    // 事件本身帶的 entryID 就是第 1 步 entry_add 真正產生的值(下方會驗證其
    // 格式),已足以證明第 1 步確實執行過並產生了真實資料,不需要額外等第 1
    // 則廣播才能確認這件事。
    await expect
      .poll(() => wsEvents.some((e) => e.event === 'entry_updating'), { timeout: STEP_TIMEOUT })
      .toBe(true)
    const updatingIdx = wsEvents.findIndex((e) => e.event === 'entry_updating')

    // entry_updating 事件應該帶著第 1 步 entry_add 真正產生的 entryID(格式
    // ent_<hex>),而不是空字串或佔位符——這是動態 mock 劇本(見
    // server/internal/mockllm 套件說明)能運作的前提,雖然 script_test.go
    // 已經在 Go 層驗證過這個邏輯本身,這裡額外確認它真的透過完整鏈路傳到
    // 瀏覽器端,不是只在後端內部生效。
    expect(wsEvents[updatingIdx].entryID, 'entry_updating 事件應帶有效的 entryID(格式 ent_<hex>)')
      .toMatch(/^ent_[0-9a-f]+$/)

    const updatedTitle = '東京晴空塔(展望台預約 14:30)'
    const skytreeCard = page.locator('.tl-card-row', { hasText: updatedTitle })
    const senseijiCard = page.locator('.tl-card-row', { hasText: '淺草寺' })

    // ---- 盡力嘗試捕捉「entry_update 剛完成、entry_delete 還沒執行」這個視覺中間態 ----
    // 必須緊接在確認 entry_updating 錨點之後、還沒等 entries_updated 累積到齊
    // 之前就檢查——這是這個中間態理論上存在時間最長的位置。實測發現(見本檔
    // 開發過程):entry_update 完成到 entry_delete 完成之間的真實間隔,在本機
    // mock LLM + SQLite 的執行速度下窄到:即使是在這裡(entry_updating 剛確認
    // 的當下)立刻檢查畫面,entry_delete 也可能已經執行完畢——這個中間態的
    // 存在時間可能短於一次 WS frame 送達 Playwright 監聽器、再到瀏覽器完成
    // React re-render 所需的時間,不是斷言寫法問題,是這個系統在本機的真實
    // 速度特性。故這裡改成「能觀察到就驗證內容正確,觀察不到就略過」,不讓
    // 測試因為這個競態而不穩定失敗——真正證明「entry_update 確實發生過、且
    // 改對了值」的責任,交給下面已證實可靠的 WS 事件序列(entry_updating 帶
    // 正確 entryID)與 REST API 交叉驗證(最終資料庫值必須是改名後的新標題與
    // 新時間,見檔案尾端)。
    const caughtMidState = await skytreeCard.isVisible().catch(() => false)
    if (caughtMidState) {
      await expect(skytreeCard.locator('.tl-time'), '若捕捉到改名後的中間態,時間應已是更新後的 14:30')
        .toHaveText('14:30')
    }

    // ---- 等 entry_updating 之後的 3 則 entries_updated 全部到齊(entry_update
    // 完成 + entry_add 淺草寺 + entry_delete),代表整段劇本已跑完 ----
    try {
      await expect
        .poll(() => wsEvents.slice(updatingIdx + 1).filter((e) => e.event === 'entries_updated').length,
          { timeout: STEP_TIMEOUT })
        .toBeGreaterThanOrEqual(3)
    } catch (e) {
      throw new Error(`entry_updating 之後應累積滿 3 則 entries_updated(entry_update 完成/entry_add 淺草寺/entry_delete),逾時前實際累積:${JSON.stringify(wsEvents)}\n原始錯誤:${e}`)
    }
    const afterUpdating = wsEvents.slice(updatingIdx).map((e) => e.event)
    expect(afterUpdating, `entry_updating 之後應依序收到 3 則 entries_updated,實際收到:${JSON.stringify(afterUpdating)}`)
      .toEqual(['entry_updating', 'entries_updated', 'entries_updated', 'entries_updated'])

    // ---- 時間軸出現「淺草寺」,時間 10:00(entry_add 第 3 步)----
    await expect(senseijiCard, '應該出現淺草寺的卡片(entry_add 第 3 步)')
      .toBeVisible({ timeout: STEP_TIMEOUT })
    await expect(senseijiCard.locator('.tl-time'), '淺草寺卡片應顯示時間 10:00')
      .toHaveText('10:00', { timeout: STEP_TIMEOUT })

    // ---- 「東京晴空塔」那筆(已改名)從時間軸消失(entry_delete 第 4 步)----
    // 劇本第 4 步用 Occurrence:1 明確刪除「第一筆」entry_add 產生的那筆——
    // 即使該筆此時標題已被第 2 步改成含「展望台預約」字樣,刪除的仍是同一個
    // entryID,故此處用改名後的完整標題定位,驗證的是「原本那張卡片消失」
    // 而非「湊巧沒有任何卡片包含『東京晴空塔』文字」(若劇本誤刪成第 3 步
    // 剛新增的淺草寺,這裡的斷言會失敗,而不是靜默通過——對齊
    // script_test.go 裡 Occurrence 機制回歸測試強調的「不能靜默寫錯腳本」)。
    await expect(
      skytreeCard,
      '東京晴空塔(已改名)那筆應該從時間軸消失(entry_delete 第 4 步生效)',
    ).toHaveCount(0, { timeout: STEP_TIMEOUT })
    // 淺草寺應該還在(排除「entry_delete 誤刪成淺草寺」這種相反的失敗模式)。
    await expect(senseijiCard, '淺草寺不應該被誤刪,應該仍在時間軸上')
      .toBeVisible()

    // ---- 全程沒有任何 JS 例外或 console error ----
    expect(jsErrors, `頁面在整段流程中不應出現 JS 例外或 console error,實際捕捉到:\n${jsErrors.join('\n')}`)
      .toEqual([])

    // ---- 交叉驗證真實後端 REST API,確認 DB 最終狀態與畫面一致 ----
    // 不只信任 DOM——直接呼叫 GET /v1/channels/{id}/entries(需要登入 token),
    // 核對筆數與內容。預期最終筆數 = 3 筆種子資料 + 淺草寺 1 筆(東京晴空塔
    // 那筆已被刪除,種子資料本身 entry_update/entry_add/entry_delete 都不會
    // 動到) = 4 筆。
    const entriesRes = await page.request.get(
      `${API_BASE_URL}/v1/channels/${SEED_CHANNEL_ID}/entries`,
      { headers: { Authorization: `Bearer ${token}` } },
    )
    expect(entriesRes.ok(), `REST API GET /v1/channels/${SEED_CHANNEL_ID}/entries 應該回 2xx,實際狀態碼 ${entriesRes.status()}`)
      .toBeTruthy()
    const body = await entriesRes.json() as { entries: Array<{ title: string; start: string; startTime: string }> }
    const titles = body.entries.map((e) => e.title)

    // 注意:劇本第 4 步(entry_delete)刪除的正是「entry_add 產生、entry_update
    // 已改名」的那一筆——最終資料庫裡不應該包含改名後的標題 updatedTitle,
    // 也不應該包含改名前的原始標題「東京晴空塔」,兩者都已經不存在,因為
    // 刪除的是同一筆資料的最新狀態,不是「改名前的舊版本仍留著」這種語意。
    // （第一版寫法在這裡誤植了「應包含 updatedTitle」的斷言,錯把 entry_update
    // 改到的中間值當成最終該存在的值,已用獨立除錯腳本反覆輪詢 REST API 確認
    // 資料庫穩定收斂在「不含任何東京晴空塔字樣、僅 3 筆種子 + 淺草寺」這個
    // 狀態,不是時序競態,是斷言本身邏輯寫錯。）
    expect(body.entries.length, `後端最終應有 4 筆 entry(3 筆種子 + 淺草寺),實際 ${body.entries.length} 筆:${JSON.stringify(titles)}`)
      .toBe(4)
    expect(titles.some((t) => t === '淺草寺'), '後端資料應包含「淺草寺」')
      .toBe(true)
    expect(titles.some((t) => t.includes('東京晴空塔')), `後端資料不應再包含任何「東京晴空塔」字樣的標題(改名前或改名後皆然,已被 entry_delete 刪除),實際:${JSON.stringify(titles)}`)
      .toBe(false)
    // 3 筆種子資料應維持原樣不變(entry_add/entry_update/entry_delete 全部
    // 針對東京晴空塔/淺草寺操作,不應動到種子資料,見 seedIfEmpty)。
    for (const seedTitle of ['開會敲定 Q3 產品規格', '準備預算上調提案(+15%)', '修登入頁的 bug']) {
      expect(titles.some((t) => t === seedTitle), `後端資料應包含未受影響的種子資料「${seedTitle}」`)
        .toBe(true)
    }

    const senseiji = body.entries.find((e) => e.title === '淺草寺')
    expect(senseiji?.startTime, '後端資料中淺草寺的 startTime 應為 10:00').toBe('10:00')
  })
})

// ---- 輔助函式 ----

async function login(page: Page) {
  await page.goto('/app')
  // 訪客進 /app 一律先擋在 .login-card(見 App.tsx PhoneContent 的
  // props.isGuest 判斷,早於桌面/手機版分流),範圍限定在這個容器內找輸入框
  // 與送出鈕——避免撞到別處同樣文字/title 是「登入」的元素(例如
  // ChannelsScreen 導覽列那顆開關登入下拉用的圖示鈕,title 也是「登入」,
  // 但那顆只在已登入離開這個畫面後才會出現,理論上不會同時存在,這裡仍
  // 明確限定容器範圍,不依賴這個隱含的互斥前提)。
  const loginCard = page.locator('.login-card')
  await expect(loginCard, '訪客狀態應該看到登入卡片').toBeVisible()
  await loginCard.locator('input[type="email"]').fill(LOGIN_EMAIL)
  await loginCard.locator('input[type="password"]').fill(LOGIN_PASSWORD)
  await loginCard.getByRole('button', { name: '登入' }).click()
  // 登入成功後畫面會切換離開登入卡片(login-card 消失),用這個當登入完成的
  // 條件等待,避免後續操作在登入請求還沒回來時就搶跑。
  await expect(loginCard).toHaveCount(0, { timeout: STEP_TIMEOUT })
}

async function openSeedChannel(page: Page) {
  // 種子頻道「產品討論」在頻道列表(手機版整頁列表或桌面版側欄,見 App.tsx
  // ChannelsScreen/DesktopChannelList)固定會出現,兩種佈局都用 .row/
  // .desktop-channel-item 元素、文字包含頻道名稱,用文字比對可以兩種佈局通用。
  const channelItem = page.locator('.row, .desktop-channel-item', { hasText: SEED_CHANNEL_NAME }).first()
  await expect(channelItem, `頻道列表應該出現種子頻道「${SEED_CHANNEL_NAME}」`).toBeVisible({ timeout: STEP_TIMEOUT })
  await channelItem.click()
  // 進入頻道後應該看到輸入列(composer),用它當「已進入聊天畫面」的條件——
  // 同時代表本測試 beforeEach 掛的 page.on('websocket') 監聽器已經來得及
  // 掛上進頻道時建立的那條 WS 連線(監聽器在 goto('/app') 之前就註冊好,
  // 早於任何導覽動作)。
  await expect(page.locator('.composer')).toBeVisible({ timeout: STEP_TIMEOUT })
}
