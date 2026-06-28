# LLM 記錄行程條目流程

## 概述

使用者在頻道發送訊息後，LLM 依序執行以下步驟，將行程資訊記錄成條目並補充地點座標。

---

## 完整流程

```
使用者輸入
  ↓
[判斷是否為可記錄事項]
  ↓ 是
[1. record_entry — 寫入條目]
  ↓
[有 location？]
  ├─ 是 → [2. list_trips — 推斷地區]
  │          ↓
  │        [3. geocode — 查詢座標]
  │          ↓
  │        [呈現地點資訊給使用者]
  │
  └─ 否 → 跳過座標流程
  ↓
[有候選行程？]
  ├─ 是 → [4. add_to_trip — 歸入行程]
  └─ 否 → 結束
```

---

## 步驟說明

### 步驟 1：record_entry — 寫入條目

呼叫 `record_entry` 將使用者輸入的事項記成條目。

**輸入規則：**
- `item`：事項描述，去掉時間與地點，只留核心描述（如「宿希爾頓嘉悅里酒店」）
- `start`：日期，以英文自然語詞填入（如 `'June 29'`），系統負責換算成絕對日期
- `startTime`：時刻，24 小時制（如 `'15:00'`），全日事件留空
- `end`：結束日期（住宿、區間性事件才填）
- `location`：地點名稱原文（如「希爾頓嘉悅里酒店」）

**回傳：**
- `entryID`：新條目的 ID
- `candidates`：時間重疊的既有行程列表（可能為空）

---

### 步驟 2：list_trips — 推斷整體地區

條目有 `location` 時，呼叫 `list_trips` 取得頻道所有行程的標題與時間範圍。

**推斷邏輯：**
- 從行程標題推斷地理位置，例如：
  - 「2025 宮古島」→ 宮古島
  - 「東京五日遊」→ 東京
  - 「北歐芬蘭之旅」→ 芬蘭
- 優先找時間上最接近該條目的行程
- 若多個行程都可能，以標題語意最相關者為準
- 若無法判斷，直接用 location 原文查詢

---

### 步驟 3：geocode — 查詢地點座標

將推斷出的城市名加在 location 前面，組成查詢字串後呼叫 `geocode`。

**組合規則：**
```
geocode(place = "{城市名}" + "{location 原文}")
```

**範例：**
| location 原文 | 推斷城市 | geocode 查詢字串 |
|---|---|---|
| 希爾頓嘉悅里酒店 | 宮古島 | `宮古島希爾頓嘉悅里酒店` |
| 築地市場 | 東京 | `東京築地市場` |
| 聖誕老人村 | 芬蘭 | `芬蘭聖誕老人村` |

**回傳：**
- `name`：地點全名
- `address`：完整地址
- `lat` / `lng`：經緯度座標

**失敗處理：**
- 查詢失敗（找不到地點）→ 跳過，不影響記錄流程
- 無法推斷城市 → 直接用 location 原文查詢

查詢成功後，將地點名稱、地址、座標呈現給使用者。

---

### 步驟 4：add_to_trip — 歸入行程（選擇性）

`record_entry` 回傳的 `candidates` 若不為空，判斷該條目是否屬於某個候選行程：

- **判斷標準：語意相關**，不只是時間重疊
  - ✅ 同一趟旅程的住宿、機票、景點 → 歸入
  - ❌ 時間碰巧重疊但不同事件 → 不歸入
- 符合條件時呼叫 `add_to_trip(entryID=..., tripID=...)`
- 無候選或都不相關則跳過

---

## 範例：「6/29 宿希爾頓嘉悅里酒店」

```
使用者輸入：6/29 宿希爾頓嘉悅里酒店

1. record_entry(
     item = "宿希爾頓嘉悅里酒店",
     start = "June 29",
     location = "希爾頓嘉悅里酒店"
   )
   → entryID = "ent_xxxx", candidates = []

2. list_trips()
   → 找到「2025 宮古島」→ 推斷地區：宮古島

3. geocode(place = "宮古島希爾頓嘉悅里酒店")
   → name: "Canopy by Hilton Okinawa Miyako Island Resort"
   → address: "Kugai-550-7 Hirara, Miyakojima, Okinawa 906-0015, Japan"
   → lat: 24.7994, lng: 125.2592

   呈現給使用者：
   📍 Canopy by Hilton Okinawa Miyako Island Resort
   地址：Kugai-550-7 Hirara, Miyakojima, Okinawa 906-0015, Japan
   座標：24.799449, 125.259220

4. candidates 為空 → 跳過 add_to_trip
```

---

## 工具一覽

| 工具 | 用途 | 必要條件 |
|---|---|---|
| `record_entry` | 寫入條目 | 使用者輸入有可記錄事項 |
| `list_trips` | 推斷地區 | 條目有 location |
| `geocode` | 查詢座標 | 條目有 location |
| `add_to_trip` | 歸入行程 | candidates 不為空且語意相符 |
