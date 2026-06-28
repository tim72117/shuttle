# 分享行程 - 流程圖

## 1. 整體分享流程

```
┌─────────────────────────────────────────────────────────────────┐
│ 行程分享系統整體流程                                             │
└─────────────────────────────────────────────────────────────────┘

分享發起者（User A）
├─ 擁有頻道（Channel A）
├─ 頻道內有行程（Trip X）
└─ 權限：該頻道的 editor

      ↓ User A 點擊「分享」

┌─────────────────────────────────┐
│ 分享方式選擇                     │
├─────────────────────────────────┤
│ 1. 分享給使用者                 │ ← 邀請 + 需接受
│ 2. 複製到我的其他頻道           │ ← 直接複製
│ 3. 生成公開連結                 │ ← 任何人可訪問
└─────────────────────────────────┘

      ↓ 選擇方式 1：分享給使用者

┌─────────────────────────────────┐
│ 創建 TripShare 記錄             │
├─────────────────────────────────┤
│ status: "pending"               │
│ target_user_id: User B          │
│ created_at: now()               │
│ share_token: null               │
└─────────────────────────────────┘

      ↓ 發送通知給 User B

接收者（User B）
├─ 收到分享通知
├─ 點擊連結訪問分享詳情
└─ 看到 Trip X 的信息

      ↓ 點擊「複製到我的頻道」

┌─────────────────────────────────┐
│ 複製流程                        │
├─────────────────────────────────┤
│ 1. User B 選擇目標頻道          │
│ 2. 系統檢查 User B 有無編輯權   │
│ 3. 複製 Trip X → 新 Trip Y      │
│ 4. 複製 Entries                │
│ 5. 標記 source_trip_id          │
└─────────────────────────────────┘

      ↓ 複製完成

┌─────────────────────────────────┐
│ 更新 TripShare 記錄             │
├─────────────────────────────────┤
│ status: "accepted"              │
│ accepted_by: User B             │
│ accepted_at: now()              │
│ destination_trip_id: Trip Y     │
│ destination_channel_id: Ch_B    │
└─────────────────────────────────┘

      ↓ 完成

User B 現在在自己頻道看到複製的行程
```

---

## 2. 詳細流程 - 三種分享方式

### 方式 1：User-to-User Sharing

```
User A                      System                      User B
  │                           │                          │
  ├──(點擊分享)──────────────→ │                          │
  │                           │                          │
  │    ┌──────────────────────┼──────────┐               │
  │    │ 輸入 User B email   │                          │
  │    │ 添加備註            │                          │
  │    │ 點擊「發送」        │                          │
  │    └──────────────────────┼──────────┘               │
  │                           │                          │
  │                   (建立 TripShare)                   │
  │                   (status=pending)                   │
  │                           │                          │
  │                  (發送通知郵件)                      │
  │                           ├─────────────────────────→│
  │                           │                    (點擊郵件)
  │                           │                          │
  │                           │    ┌──────────────────┐  │
  │                           │    │分享詳情頁面      │  │
  │                           │    │- Trip 標題       │  │
  │                           │    │- Entries 清單   │  │
  │                           │    │- 分享者信息     │  │
  │                           │    │[複製按鈕]       │  │
  │                           │    └──────────────────┘  │
  │                           │                          │
  │                           │                    (選擇頻道)
  │                           │                          │
  │                           │    ┌──────────────────┐  │
  │                           │    │頻道選擇對話框  │  │
  │                           │    │[Channel A]       │  │
  │                           │    │[Channel B]       │  │
  │                           │    │[Channel C]       │  │
  │                           │    └──────────────────┘  │
  │                           │                          │
  │                           │                    (確認複製)
  │                           │                          │
  │                  (複製 Trip + Entries)              │
  │                  (建立 destination_trip_id)         │
  │                           │                          │
  │                  (更新 TripShare status=accepted)    │
  │                           │                          │
  │                           │       (顯示新 Trip)     │
  │                           │←────────────────────────│
  │                           │                          │
  │       (分享者收到通知：已被複製)                    │
  │←──────────────────────────│                          │
```

### 方式 2：Channel-to-Channel Copy

```
User A (在 Channel A)
  │
  ├─[分享按鈕]
  │
  ├─[選擇「複製到其他頻道」]
  │
  ├─[選擇目標頻道 Channel B]
  │
  ├─[確認]
  │
  → 系統檢查：User A 是否有 Channel B 的編輯權
  │
  ├─ YES → 直接複製
  │        ├─ 複製 Trip
  │        ├─ 複製 Entries
  │        └─ 標記 source_trip_id
  │        → 完成，無需確認
  │
  └─ NO → 返回错誤「無權限」
```

### 方式 3：Public Link Sharing

```
User A
  │
  ├─[分享按鈕]
  │
  ├─[選擇「生成公開連結」]
  │
  ├─[設置過期時間]
  │   ├─ 1小時
  │   ├─ 1天
  │   ├─ 1週
  │   └─ 永不過期
  │
  ├─[生成]
  │
  → 系統生成：
  │   ├─ share_token: "abc123xyz"
  │   ├─ share_url: "https://channel.app/share/abc123xyz"
  │   └─ expires_at: timestamp
  │
  ├─[複製連結]
  │
  → User A 可通過任何途徑分享此連結（短信、社群媒體等）

任何人訪問 /share/abc123xyz
  │
  ├─ 檢查 expires_at
  │   ├─ 已過期？ → 顯示「連結已過期」
  │   └─ 有效？   → 顯示匿名分享頁面
  │
  ├─ 顯示：
  │   ├─ Trip 標題、日期、地點
  │   ├─ Entries 清單（不含敏感訊息）
  │   ├─ 分享者名稱（可選）
  │   └─ [複製到我的頻道] (若已登入)
  │       └─ 需選擇目標頻道 + 確認
```

---

## 3. 數據流轉流程

```
Trip X (Channel A, User A)
├─ id: "trip_001"
├─ title: "東京之旅"
├─ start: "2026-07-01"
├─ end: "2026-07-07"
└─ entries:
   ├─ Entry 1 (飛機)
   ├─ Entry 2 (飯店)
   └─ Entry 3 (景點)

     ↓ User A 分享給 User B

[TripShare 記錄建立]
├─ id: "share_001"
├─ source_trip_id: "trip_001"
├─ target_user_id: "user_b"
├─ status: "pending"
└─ share_token: null

     ↓ User B 接受並複製

Trip Y (Channel B, User B) [新 Trip]
├─ id: "trip_002"
├─ title: "東京之旅" (複製的標題)
├─ start: "2026-07-01"
├─ end: "2026-07-07"
├─ source_trip_id: "trip_001" [★ 指向原始]
├─ source_channel_id: "ch_a"
├─ source_user_id: "user_a"
└─ entries:
   ├─ Entry 4 (飛機) [shared_from_entry_id: "entry_1"]
   ├─ Entry 5 (飯店) [shared_from_entry_id: "entry_2"]
   └─ Entry 6 (景點) [shared_from_entry_id: "entry_3"]

[TripShare 記錄更新]
├─ status: "accepted"
├─ accepted_by: "user_b"
├─ accepted_at: 2026-06-28 10:30:00
├─ destination_trip_id: "trip_002"
└─ destination_channel_id: "ch_b"

後續：User B 可在自己的頻道獨立編輯 Trip Y
      不影響 User A 的 Trip X
```

---

## 4. 權限檢查流程

```
使用者執行分享操作
  │
  ├─ 檢查：該使用者是否擁有 Trip 所在頻道的編輯權？
  │   ├─ YES → 允許分享
  │   └─ NO  → 返回「無權限」錯誤
  │
  ├─ 如果分享類型是「分享給使用者」
  │   ├─ 檢查：目標使用者是否存在？
  │   │   ├─ YES → 創建待命分享
  │   │   └─ NO  → 返回「使用者不存在」
  │   │
  │   └─ 發送通知
  │
  ├─ 如果分享類型是「複製到頻道」
  │   ├─ 檢查：該使用者是否為目標頻道成員？
  │   │   ├─ YES → 檢查權限
  │   │   └─ NO  → 返回「非頻道成員」
  │   │
  │   └─ 檢查：該使用者是否有目標頻道的編輯權？
  │       ├─ YES → 直接複製
  │       └─ NO  → 返回「無編輯權」
  │
  └─ 如果分享類型是「公開連結」
     └─ 生成 token + 設定過期時間
        └─ 任何人可訪問（無權限檢查）
```

---

## 5. 狀態機（State Machine）

```
                建立
                 │
                 ▼
            ┌─────────┐
            │ pending │ ← 等待接收者回應
            └────┬────┘
                 │
         ┌───────┴────────┐
         │                │
         ▼                ▼
    ┌─────────┐     ┌─────────┐
    │accepted │     │declined │ ← 接收者拒絕
    └────┬────┘     └─────────┘
         │
         ▼
    ┌─────────┐
    │ active  │ ← 已複製，分享關係建立
    └────┬────┘
         │
         ▼
    ┌─────────┐
    │revoked  │ ← 分享者撤銷（可選功能）
    └─────────┘

公開連結特殊狀態：
    created → active → expired (當 expires_at < now())
```

---

## 6. 通知流程

```
分享發起 → 創建 TripShare

  ├─ User-to-User 分享
  │  ├─ 發送郵件通知接收者
  │  ├─ 郵件內容：
  │  │  ├─ "User A 分享了行程『東京之旅』給你"
  │  │  ├─ Trip 簡介（時間、地點等）
  │  │  ├─ 分享者備註（如有）
  │  │  └─ [查看詳情] 按鈕 → 含 share_token 的連結
  │  │
  │  └─ 接收者點擊郵件連結
  │     └─ 訪問 /shares/{shareToken}
  │        └─ 顯示分享詳情頁面
  │
  └─ Channel-to-Channel 複製
     └─ 無通知（直接複製）
     
接收者響應 → 更新 TripShare 狀態

  ├─ 複製成功
  │  ├─ 更新 status=accepted
  │  ├─ 記錄到 TripShareHistory
  │  └─ 發送確認通知給分享者
  │
  └─ 拒絕複製
     ├─ 更新 status=declined
     └─ 記錄到 TripShareHistory
```

---

## 7. 審計日誌示例

```
TripShare ID: share_001

Timeline:
1. 2026-06-28 10:00:00
   Action: created
   Actor: user_a
   Details: {
     share_type: "user",
     target_user_id: "user_b",
     message: "你可能也會喜歡這個行程！"
   }

2. 2026-06-28 10:05:00
   Action: sent_notification
   Actor: system
   Details: {
     notification_type: "email",
     sent_to: "user_b@email.com"
   }

3. 2026-06-28 14:30:00
   Action: accepted
   Actor: user_b
   Details: {
     target_channel_id: "ch_b"
   }

4. 2026-06-28 14:31:00
   Action: copied
   Actor: system
   Details: {
     destination_trip_id: "trip_002",
     destination_channel_id: "ch_b",
     entries_copied: 3
   }

5. 2026-06-29 09:00:00
   Action: viewed
   Actor: user_a
   Details: {
     view_count: 1
   }
```

---

## 實施步驟

### Database
- [ ] 創建 trip_shares 表
- [ ] 創建 trip_share_history 表
- [ ] 修改 trips 表（添加 source_* 欄位）
- [ ] 創建索引

### Backend API
- [ ] POST /channels/{id}/trips/{id}/shares
- [ ] GET /trip-shares/{id}
- [ ] POST /trip-shares/{id}/accept
- [ ] POST /trip-shares/{id}/decline
- [ ] DELETE /trip-shares/{id}
- [ ] GET /channels/{id}/trip-shares
- [ ] GET /share/{shareToken}

### Frontend
- [ ] 分享按鈕 UI
- [ ] 分享對話框組件
- [ ] 分享詳情頁面
- [ ] 公開分享頁面
- [ ] 通知顯示

### Testing
- [ ] Unit tests (permission checks)
- [ ] Integration tests (sharing flow)
- [ ] E2E tests (UI flows)
