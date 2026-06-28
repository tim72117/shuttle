# 分享行程（Trip Sharing）功能設計

## 概述

允許使用者將行程（Trip）分享給其他使用者或複製到其他頻道。支援權限管理、分享歷史追蹤。

---

## 功能流程

```
使用者 A
  ↓
[選擇 Trip]
  ↓
[分享方式選擇]
  ├─ 方式 1：分享給另一個使用者（邀請連結）
  ├─ 方式 2：分享到另一個頻道（需要成員權限）
  └─ 方式 3：生成公開分享連結（限時或永久）
  ↓
[建立分享記錄]
  ↓
使用者 B
  ↓
[接收分享通知或訪問連結]
  ↓
[複製或導入 Trip]
  ↓
[在自己的頻道生成新 Trip（帶原始來源標記）]
```

---

## 數據庫設計

### 1. **TripShare 表** - 分享記錄

```sql
CREATE TABLE trip_shares (
  id TEXT PRIMARY KEY,
  
  -- 來源
  source_trip_id TEXT NOT NULL,
  source_channel_id TEXT NOT NULL,
  source_user_id TEXT NOT NULL,  -- 分享者
  
  -- 目標
  share_type TEXT NOT NULL,  -- "user" | "channel" | "public"
  target_user_id TEXT,       -- 如果是分享給使用者
  target_channel_id TEXT,    -- 如果是分享到頻道
  
  -- 分享連結
  share_token TEXT UNIQUE,   -- 短 token，用於連結訪問 (share_type='public' 時使用)
  expires_at TIMESTAMP,      -- 連結過期時間 (null = 永不過期)
  
  -- 狀態
  status TEXT NOT NULL,      -- "pending" | "accepted" | "declined" | "copied" | "active"
  accepted_at TIMESTAMP,     -- 接收者接受時間
  accepted_by TEXT,          -- 實際接收的使用者
  
  -- 衍生的複製 Trip
  destination_trip_id TEXT,  -- 接收者複製後在自己頻道生成的 Trip ID
  destination_channel_id TEXT, -- 新 Trip 所在頻道
  
  -- 中繼資料
  message TEXT,              -- 分享時的備註
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  
  FOREIGN KEY (source_trip_id) REFERENCES trips(id),
  FOREIGN KEY (source_channel_id) REFERENCES channels(id),
  FOREIGN KEY (target_channel_id) REFERENCES channels(id),
  INDEX idx_source (source_trip_id),
  INDEX idx_target_user (target_user_id),
  INDEX idx_target_channel (target_channel_id),
  INDEX idx_share_token (share_token)
);
```

### 2. **TripShareHistory 表** - 分享審計日誌

```sql
CREATE TABLE trip_share_history (
  id TEXT PRIMARY KEY,
  share_id TEXT NOT NULL,
  
  action TEXT NOT NULL,  -- "created" | "accepted" | "declined" | "copied" | "revoked"
  actor_id TEXT NOT NULL, -- 執行動作的使用者
  
  details JSON,  -- 額外的動作詳情
  
  created_at TIMESTAMP NOT NULL,
  
  FOREIGN KEY (share_id) REFERENCES trip_shares(id),
  INDEX idx_share (share_id),
  INDEX idx_actor (actor_id)
);
```

### 3. **修改 Trip 表** - 添加來源追蹤

```sql
-- 在現有 Trip 表添加欄位
ALTER TABLE trips ADD COLUMN (
  source_trip_id TEXT,        -- 如果是複製而來，指向原始 Trip
  source_user_id TEXT,        -- 原始分享者
  source_channel_id TEXT,     -- 原始頻道
  shared_at TIMESTAMP,        -- 被分享的時間
  
  FOREIGN KEY (source_trip_id) REFERENCES trips(id)
);
```

### 4. **修改 Entry 表** - 分享標記

```sql
-- 在現有 Entry 表添加欄位（可選，用於追蹤條目來源）
ALTER TABLE entries ADD COLUMN (
  shared_from_entry_id TEXT,  -- 如果是從分享的 Trip 複製而來
  
  FOREIGN KEY (shared_from_entry_id) REFERENCES entries(id)
);
```

---

## API 端點設計

### 分享相關

```
POST /v1/channels/{channelID}/trips/{tripID}/shares
  創建分享
  Request: { shareType: "user|channel|public", targetUserID?: "", targetChannelID?: "", message?: "", expiresAt?: "" }
  Response: { shareID, shareToken, shareURL }

GET /v1/trip-shares/{shareID}
  獲取分享詳情（分享者或接收者可見）
  Response: TripShare

POST /v1/trip-shares/{shareID}/accept
  接收者接受分享（複製 Trip 到自己的頻道）
  Request: { targetChannelID }
  Response: { destinationTripID, destinationChannelID }

POST /v1/trip-shares/{shareID}/decline
  接收者拒絕分享
  Response: { status: "declined" }

DELETE /v1/trip-shares/{shareID}
  分享者撤銷分享
  Response: 200

GET /v1/channels/{channelID}/trip-shares?role=incoming|outgoing
  列出該頻道的分享紀錄
  Response: { shares: [TripShare] }

GET /share/{shareToken}
  公開分享連結訪問
  Response: Trip + Entries（匿名或受限可見）
```

---

## 前端流程

### 分享發起

```
User A 在頻道看到 Trip
  ↓
[點擊分享按鈕]
  ↓
[彈出分享對話框]
  分享方式選擇：
  ├─ 分享給使用者
  │  └─ 輸入使用者 Email / ID
  │  └─ 可選備註
  │  └─ 點擊「發送邀請」
  │
  ├─ 複製到頻道
  │  └─ 選擇目標頻道（自己是成員的）
  │  └─ 直接複製（無需接收者確認）
  │
  └─ 生成公開連結
     └─ 設定過期時間（1小時 / 1天 / 永久）
     └─ 複製連結分享
```

### 分享接收

#### 方式 1：邀請郵件
```
使用者 B 收到郵件：
  "User A 分享了行程『東京之旅』給你"
  
  ↓ 點擊連結（包含 shareToken）
  
  [Web 顯示分享詳情]
  ├─ Trip 標題、時間、地點
  ├─ Entry 清單預覽
  └─ 按鈕：「複製到我的頻道」或「不複製」

  ↓ 點擊「複製到我的頻道」
  
  [選擇目標頻道]
  
  ↓ 確認
  
  [後端複製 Trip + Entries，帶來源標記]
  
  [前端顯示新 Trip 位置]
```

#### 方式 2：公開連結
```
任何人訪問 /share/{shareToken}

↓ 顯示匿名分享頁面

[Trip + Entries 預覽（不含敏感訊息）]
├─ 如果訪問者已登入：「複製到我的頻道」
├─ 如果未登入：「登入後複製」
└─ 複製按鈕
```

---

## 權限與安全

### 分享權限

| 場景 | 權限需求 |
|------|---------|
| 分享自己頻道的 Trip 給他人 | 是該 Trip 所在頻道的 editor |
| 複製他人分享的 Trip 到自己頻道 | 是目標頻道的 editor |
| 訪問公開分享連結 | 無要求（任何人可訪問） |
| 撤銷分享 | 原始分享者 |
| 設置分享過期 | 原始分享者 |

### 數據隱私

- **User-to-User 分享**：接收者同意後才能見到 Trip 內容
- **Channel 內複製**：需要 editor 權限
- **公開連結**：只顯示非敏感訊息（無使用者聯絡方式），可設定過期
- **審計日誌**：所有分享/複製動作記錄

---

## 資料流轉

### 複製時的處理

```
原始 Trip (Channel A)
  ├─ Entry 1 (Location: Tokyo)
  ├─ Entry 2 (Location: Kyoto)
  └─ Entry 3 (Location: Osaka)

  ↓ User B 接受分享，複製到 Channel B

新 Trip (Channel B) ← 帶有來源標記
  ├─ source_trip_id: "trip_xxx" (原始 Trip ID)
  ├─ source_channel_id: "ch_a" (原始頻道)
  ├─ source_user_id: "user_a" (分享者)
  ├─ shared_at: 2026-06-28
  │
  ├─ Entry 1 (複製，shared_from_entry_id: "entry_xxx")
  ├─ Entry 2 (複製)
  └─ Entry 3 (複製)

後續編輯：User B 在自己的頻道編輯 Entry，不影響原始頻道
```

---

## 後續考量

- **協作編輯**：分享後保持同步（需要 merge 邏輯）
- **評論系統**：在分享的 Trip 上留評論
- **版本控制**：追蹤 Trip 的修改歷史
- **推薦系統**：根據分享熱度推薦行程
- **費用共享**：計算分享的 Trip 中的共同費用

---

## 實現優先級

### Phase 1（MVP）
- [x] 數據庫設計
- [ ] API：建立分享記錄
- [ ] API：接收/接受分享
- [ ] API：複製 Trip
- [ ] 前端：分享對話框
- [ ] 前端：分享頁面

### Phase 2
- [ ] 公開連結 + 過期機制
- [ ] 郵件通知
- [ ] 分享歷史查看

### Phase 3
- [ ] 權限細緻化
- [ ] 協作編輯
- [ ] 費用共享計算
