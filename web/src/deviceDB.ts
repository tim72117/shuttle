// 裝置端 DB —— 與 server 隔離的本地原話儲存(對齊 iOS LocalStore)。
//
// 架構:sql.js(SQLite WASM,記憶體)+ IndexedDB 持久化。
//   - 原話(message)只存這裡,後端不保存(local-first)。
//   - sql.js 是記憶體 DB,故每次寫入後把整個 DB dump 成 bytes 存進 IndexedDB;
//     啟動時從 IndexedDB 讀回,重建記憶體 DB。
//   - schema 對齊 iOS LocalStore 的 messages 表(id/channel_id/author_id/author_name/text/created_at)。

import initSqlJs from 'sql.js'
import type { Database, SqlJsStatic } from 'sql.js'
import wasmUrl from 'sql.js/dist/sql-wasm.wasm?url'
import type { Message } from './types'
import type { TripBatches, TripEntry } from './clienttools/tripEntryTools'
import type { AssistPlace } from './api'

const IDB_NAME = 'channel-device-db'
const IDB_STORE = 'sqlite'
const IDB_KEY = 'messages.sqlite'

const SCHEMA = `
CREATE TABLE IF NOT EXISTS messages (
  id          TEXT PRIMARY KEY,
  channel_id  TEXT NOT NULL,
  author_id   TEXT NOT NULL,
  author_name TEXT NOT NULL,
  text        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, created_at);

-- clienttools 多批次旅程清單(見 ChatScreen.tsx clientToolsBatches / TripBatches
-- 型別)。純前端記憶體資料的持久化落地,與後端 entries 表無關、不同步——
-- ClientToolsBridge 每次工具執行後回傳的是某個 key「整批最新清單」而非單筆
-- 增量,故用 replaceTripBatch 整批覆寫而非逐筆 upsert,rowid 隱含順序即維持
-- 該次回傳的清單順序。key 欄位讓同一個 channel 能同時存多個獨立批次
-- (LLM 透過 trip_entry_add/update 自訂的語意化 key,或 ENTRY_QUERY_BATCH_KEY
-- 這個 entry_query 專用的固定保留 key,見 ChatScreen.tsx 該常數宣告處的說明
-- ——統一走這張表、這套 API,不另外維護一套平行邏輯)。
--
-- 表名特意取新名(trip_batches,而非沿用舊的 trip_entries)而非做 ALTER TABLE
-- migration:sql.js 是每次啟動從 IndexedDB 讀回 bytes 重建的記憶體 DB,
-- CREATE TABLE IF NOT EXISTS 對已存在的舊 schema(沒有 key 欄位)不會自動
-- 加欄位;這是本地暫存性質的裝置端 DB、功能仍在開發中未上線,改表名讓舊資料
-- (舊 trip_entries 表,若殘留在使用者瀏覽器裡)自然作廢、新 schema 直接生效,
-- 不需要處理資料遷移。
CREATE TABLE IF NOT EXISTS trip_batches (
  channel_id  TEXT NOT NULL,
  key         TEXT NOT NULL,
  id          TEXT NOT NULL,
  title       TEXT NOT NULL,
  date        TEXT NOT NULL,
  time        TEXT NOT NULL,
  note        TEXT NOT NULL,
  PRIMARY KEY (channel_id, key, id)
);

-- recommend_nearby 工具查到、掛在某則答案訊息底下顯示的候選景點清單
-- (見 ChatScreen.tsx ChatMessage.recommendedPlaces)。一則訊息可能對應多筆
-- 景點(一對多),與 trip_batches「整批覆寫單一批次」不同,這裡用
-- message_id 分組、rowid 隱含順序;寫入時比照 replaceTripBatch 的模式
-- 先刪除該 message_id 底下的舊資料再整批插入,確保不會重複或留下孤兒資料。
CREATE TABLE IF NOT EXISTS message_recommended_places (
  message_id   TEXT NOT NULL,
  name         TEXT NOT NULL,
  address      TEXT NOT NULL,
  lat          REAL NOT NULL,
  lng          REAL NOT NULL,
  primary_type TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_message_recommended_places_msg ON message_recommended_places(message_id);

-- 訊息底下觸發顯示的旅程清單批次 key(見 ChatScreen.tsx ChatMessage.tripListTriggered)。
-- 一則答案訊息可能觸發多個 key(一對多),與 message_recommended_places 同樣的
-- 模式:message_id 分組、rowid 隱含順序,寫入時先刪該 message_id 底下的舊資料
-- 再整批插入。只存 key 字串本身,不存清單內容——內容一律從 trip_batches 表
-- (透過 key)即時讀取當下最新值,對齊「清單永遠顯示目前最新完整內容」的既有
-- 設計(見 MessageBubble 的 tripBatches prop 說明)。
CREATE TABLE IF NOT EXISTS message_trip_list_keys (
  message_id TEXT NOT NULL,
  key        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_message_trip_list_keys_msg ON message_trip_list_keys(message_id);
`

let SQL: SqlJsStatic | null = null
let db: Database | null = null
let ready: Promise<void> | null = null

// ---- IndexedDB 低階存取(只存一塊 SQLite bytes)----

function idbOpen(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(IDB_NAME, 1)
    req.onupgradeneeded = () => req.result.createObjectStore(IDB_STORE)
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

async function idbLoad(): Promise<Uint8Array | null> {
  const idb = await idbOpen()
  return new Promise((resolve, reject) => {
    const tx = idb.transaction(IDB_STORE, 'readonly')
    const req = tx.objectStore(IDB_STORE).get(IDB_KEY)
    req.onsuccess = () => resolve((req.result as Uint8Array) ?? null)
    req.onerror = () => reject(req.error)
  })
}

async function idbSave(bytes: Uint8Array): Promise<void> {
  const idb = await idbOpen()
  return new Promise((resolve, reject) => {
    const tx = idb.transaction(IDB_STORE, 'readwrite')
    tx.objectStore(IDB_STORE).put(bytes, IDB_KEY)
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
  })
}

// ---- 初始化 ----

// init 載入 sql.js、從 IndexedDB 還原既有 DB(或建新的),套用 schema。
// 多次呼叫共用同一個 Promise(冪等)。
export function initDeviceDB(): Promise<void> {
  if (ready) return ready
  ready = (async () => {
    SQL = await initSqlJs({ locateFile: () => wasmUrl })
    const saved = await idbLoad().catch(() => null)
    db = saved ? new SQL.Database(saved) : new SQL.Database()
    db.run(SCHEMA)
  })()
  return ready
}

// persist 把目前記憶體 DB dump 成 bytes 存進 IndexedDB。
async function persist(): Promise<void> {
  if (!db) return
  await idbSave(db.export())
}

// ---- 原話讀寫 API ----

// saveMessage 寫入(或覆寫)一則原話,並持久化。
export async function saveMessage(m: Message): Promise<void> {
  await initDeviceDB()
  if (!db) return
  db.run(
    `INSERT OR REPLACE INTO messages (id, channel_id, author_id, author_name, text, created_at)
     VALUES (?, ?, ?, ?, ?, ?)`,
    [m.id, m.channelID, m.authorID, m.authorName, m.text, m.createdAt],
  )
  await persist()
}

// listMessages 回傳某頻道的原話,依時間舊到新。
export async function listMessages(channelID: string): Promise<Message[]> {
  await initDeviceDB()
  if (!db) return []
  const res = db.exec(
    `SELECT id, channel_id, author_id, author_name, text, created_at
     FROM messages WHERE channel_id = ? ORDER BY created_at ASC`,
    [channelID],
  )
  if (res.length === 0) return []
  return res[0].values.map((row) => ({
    id: row[0] as string,
    channelID: row[1] as string,
    authorID: row[2] as string,
    authorName: row[3] as string,
    text: row[4] as string,
    createdAt: row[5] as string,
  }))
}

// ---- 旅程清單(多批次,clienttools 用)讀寫 API ----

// replaceTripBatch 用給定的整批清單覆寫某頻道底下某一批次(key)目前存的
// 旅程清單(先刪舊、再依序插入新的)。呼叫端(ChatScreen 的 send()/
// onEntriesChange/entries_loaded 事件)每次拿到的都是該 key 的完整最新清單,
// 不是單筆增量,故用整批覆寫而非逐筆 upsert——這樣才能正確反映刪除(清單裡
// 消失的那筆會在覆寫時一併被移除)。統一給所有批次(含 ENTRY_QUERY_BATCH_KEY
// 這個 entry_query 專用的固定保留 key)共用同一套 API,不另外維護一套平行
// 邏輯(見 ChatScreen.tsx 該常數宣告處的說明)。
export async function replaceTripBatch(channelID: string, key: string, entries: TripEntry[]): Promise<void> {
  await initDeviceDB()
  if (!db) return
  db.run('DELETE FROM trip_batches WHERE channel_id = ? AND key = ?', [channelID, key])
  for (const e of entries) {
    db.run(
      `INSERT INTO trip_batches (channel_id, key, id, title, date, time, note)
       VALUES (?, ?, ?, ?, ?, ?, ?)`,
      [channelID, key, e.id, e.title, e.date, e.time, e.note],
    )
  }
  await persist()
}

// listAllTripBatches 撈回某頻道目前存的所有批次,回傳 key -> 清單(依寫入
// 順序,rowid)的對照表,供 ChatScreen 的 load() 一次性還原 clientToolsBatches
// 的初始值(取代先前只處理 ENTRY_QUERY_BATCH_KEY 單一批次的做法)。
export async function listAllTripBatches(channelID: string): Promise<TripBatches> {
  await initDeviceDB()
  const result: TripBatches = {}
  if (!db) return result
  const res = db.exec(
    `SELECT key, id, title, date, time, note FROM trip_batches WHERE channel_id = ? ORDER BY key, rowid ASC`,
    [channelID],
  )
  if (res.length === 0) return result
  for (const row of res[0].values) {
    const key = row[0] as string
    const entry: TripEntry = {
      id: row[1] as string,
      title: row[2] as string,
      date: row[3] as string,
      time: row[4] as string,
      note: row[5] as string,
    }
    const list = result[key]
    if (list) list.push(entry)
    else result[key] = [entry]
  }
  return result
}

// ---- 訊息附帶的推薦景點(recommend_nearby)讀寫 API ----

// saveMessageRecommendedPlaces 用給定的整批清單覆寫某則訊息底下的推薦景點
// (先刪舊、再依序插入新的),比照 replaceTripBatch 的整批覆寫模式——
// 一則訊息的推薦景點結果來自單次 assist 回應,不會有增量更新的情境。
export async function saveMessageRecommendedPlaces(messageID: string, places: AssistPlace[]): Promise<void> {
  await initDeviceDB()
  if (!db) return
  db.run('DELETE FROM message_recommended_places WHERE message_id = ?', [messageID])
  for (const p of places) {
    db.run(
      `INSERT INTO message_recommended_places (message_id, name, address, lat, lng, primary_type)
       VALUES (?, ?, ?, ?, ?, ?)`,
      [messageID, p.name, p.address, p.lat, p.lng, p.primaryType],
    )
  }
  await persist()
}

// listMessageRecommendedPlaces 一次撈回多則訊息各自的推薦景點,回傳
// message_id -> 清單(依寫入順序)的對照表。設計成批次查詢(而非逐則訊息各
// 查一次)是為了對齊 listMessages「一次撈整個頻道」的呼叫慣例——載入頻道
// 時只需一次資料庫往返,而非每則訊息各自查一次。清單中沒有推薦景點的訊息
// 不會出現在回傳的 Map 裡(呼叫端應以 `?? []` 收斂)。
export async function listMessageRecommendedPlaces(messageIDs: string[]): Promise<Map<string, AssistPlace[]>> {
  await initDeviceDB()
  const result = new Map<string, AssistPlace[]>()
  if (!db || messageIDs.length === 0) return result
  const placeholders = messageIDs.map(() => '?').join(', ')
  const res = db.exec(
    `SELECT message_id, name, address, lat, lng, primary_type
     FROM message_recommended_places WHERE message_id IN (${placeholders}) ORDER BY message_id, rowid ASC`,
    messageIDs,
  )
  if (res.length === 0) return result
  for (const row of res[0].values) {
    const messageID = row[0] as string
    const place: AssistPlace = {
      name: row[1] as string,
      address: row[2] as string,
      lat: row[3] as number,
      lng: row[4] as number,
      primaryType: row[5] as string,
    }
    const list = result.get(messageID)
    if (list) list.push(place)
    else result.set(messageID, [place])
  }
  return result
}

// ---- 訊息附帶的旅程清單觸發 key(ChatMessage.tripListTriggered)讀寫 API ----

// saveMessageTripListKeys 用給定的 key 清單覆寫某則訊息底下觸發顯示的批次
// 標記(先刪舊、再依序插入新的),比照 saveMessageRecommendedPlaces 的整批
// 覆寫模式——一則訊息這一輪觸發的 key 清單來自單次 assist 回應,不會有
// 增量更新的情境。只存 key 字串,不存清單內容(內容一律即時從 trip_batches
// 表讀取當下最新值,見 message_trip_list_keys 表宣告處的說明)。
export async function saveMessageTripListKeys(messageID: string, keys: string[]): Promise<void> {
  await initDeviceDB()
  if (!db) return
  db.run('DELETE FROM message_trip_list_keys WHERE message_id = ?', [messageID])
  for (const key of keys) {
    db.run(
      `INSERT INTO message_trip_list_keys (message_id, key) VALUES (?, ?)`,
      [messageID, key],
    )
  }
  await persist()
}

// listMessageTripListKeys 一次撈回多則訊息各自觸發的批次 key 清單,回傳
// message_id -> key 清單(依寫入順序)的對照表。比照 listMessageRecommendedPlaces
// 設計成批次查詢,載入頻道時只需一次資料庫往返。清單中沒有觸發任何批次的
// 訊息不會出現在回傳的 Map 裡(呼叫端應以 `?? []` 或不設定該欄位收斂)。
export async function listMessageTripListKeys(messageIDs: string[]): Promise<Map<string, string[]>> {
  await initDeviceDB()
  const result = new Map<string, string[]>()
  if (!db || messageIDs.length === 0) return result
  const placeholders = messageIDs.map(() => '?').join(', ')
  const res = db.exec(
    `SELECT message_id, key FROM message_trip_list_keys WHERE message_id IN (${placeholders}) ORDER BY message_id, rowid ASC`,
    messageIDs,
  )
  if (res.length === 0) return result
  for (const row of res[0].values) {
    const messageID = row[0] as string
    const key = row[1] as string
    const list = result.get(messageID)
    if (list) list.push(key)
    else result.set(messageID, [key])
  }
  return result
}
