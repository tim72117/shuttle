// Package model - Sharing 數據結構定義
package model

import "database/sql/driver"
import "encoding/json"
import "errors"
import "time"

// PublicLink 是公開連結
// 簡化版：頻道所有者可生成公開連結，任何人無需登入即可訪問
type PublicLink struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	ChannelID string    `json:"channelID" gorm:"uniqueIndex"`
	LinkToken string    `json:"linkToken" gorm:"uniqueIndex"` // 短 ID，如 ch_abc123xyz
	CreatedBy string    `json:"createdBy"`                    // owner/editor
	CreatedAt time.Time `json:"createdAt"`
}

// PublicLinkResponse 公開訪問的回應結構
type PublicLinkResponse struct {
	Channel Channel `json:"channel"`
	Trips   []Trip  `json:"trips"`
	Entries []Entry `json:"entries"`
}

// ChannelShare 是頻道分享記錄（已棄用，保留以相容性）
// 允許用戶通過唯一的分享連結分享整個頻道，無需登入即可訪問
type ChannelShare struct {
	ID string `json:"id" gorm:"primaryKey"`

	// 被分享的頻道
	ChannelID string `json:"channelID" gorm:"uniqueIndex"`

	// 分享者
	CreatedBy string `json:"createdBy"` // 頻道 owner/editor

	// 分享連結
	ShareToken string `json:"shareToken" gorm:"uniqueIndex"` // 短 token
	ShareURL   string `json:"shareURL"`                      // 完整 URL

	// 過期控制
	ExpiresAt *time.Time `json:"expiresAt,omitempty" gorm:"index"` // null = 永不過期
	IsActive  bool       `json:"isActive"`                         // 可手動停用

	// 訪問控制
	RequireAuth     bool         `json:"requireAuth"`     // 是否需要登入
	AccessibleRoles *StringArray `json:"accessibleRoles"` // JSON: ["editor", "viewer"] 或 nil (所有人)

	// 統計
	ViewCount      int        `json:"viewCount"`
	LastAccessedAt *time.Time `json:"lastAccessedAt,omitempty"`

	// 時間戳
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ChannelShareAccessLog 是訪問審計日誌（可選）
type ChannelShareAccessLog struct {
	ID string `json:"id" gorm:"primaryKey"`

	ShareID string `json:"shareID" gorm:"index"` // 參考 ChannelShare.ID

	// 訪問者信息
	UserID    *string `json:"userID,omitempty"` // null = 匿名訪問
	IPAddress string  `json:"ipAddress,omitempty"`
	UserAgent string  `json:"userAgent,omitempty"`

	// 訪問詳情
	AccessedAt      time.Time `json:"accessedAt"`
	DurationSeconds *int      `json:"durationSeconds,omitempty"`
}

// StringArray 自訂類型：用於 JSON 序列化字串陣列
type StringArray []string

func (a StringArray) Value() (driver.Value, error) {
	return json.Marshal(a)
}

func (a *StringArray) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion failed")
	}
	return json.Unmarshal(bytes, &a)
}

// ChannelShareResponse 是公開分享頁面的回應結構
type ChannelShareResponse struct {
	Channel   Channel   `json:"channel"`
	Trips     []Trip    `json:"trips"`
	Entries   []Entry   `json:"entries"`
	ShareInfo ShareInfo `json:"shareInfo"`
}

// ShareInfo 分享信息
type ShareInfo struct {
	SharedBy  string     `json:"sharedBy"` // 分享者名稱（不含 ID）
	SharedAt  time.Time  `json:"sharedAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// TripShare 是行程分享記錄（已棄用，改用 ChannelShare）
// 保留以相容性，後續可移除
type TripShare struct {
	ID string `json:"id" gorm:"primaryKey"`

	// 來源：分享的行程信息
	SourceTripID    string `json:"sourceTripID" gorm:"index"`
	SourceChannelID string `json:"sourceChannelID" gorm:"index"`
	SourceUserID    string `json:"sourceUserID"` // 分享者

	// 分享類型及目標
	ShareType string `json:"shareType"` // "user" | "channel" | "public"

	// user 類型：分享給特定使用者
	TargetUserID string `json:"targetUserID,omitempty" gorm:"index"`

	// channel 類型：分享到特定頻道
	TargetChannelID string `json:"targetChannelID,omitempty" gorm:"index"`

	// public 類型：生成公開連結
	ShareToken string     `json:"shareToken,omitempty" gorm:"uniqueIndex"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"` // 連結過期時間 (null = 永不過期)

	// 分享狀態
	Status     string     `json:"status"` // "pending" | "accepted" | "declined" | "active"
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	AcceptedBy string     `json:"acceptedBy,omitempty"` // 實際接受的使用者

	// 複製結果：接收者複製到自己頻道後的新 Trip
	DestinationTripID    string `json:"destinationTripID,omitempty"`
	DestinationChannelID string `json:"destinationChannelID,omitempty"`

	// 中繼資料
	Message   string    `json:"message,omitempty"` // 分享備註
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TripShareHistory 是分享審計日誌
type TripShareHistory struct {
	ID string `json:"id" gorm:"primaryKey"`

	ShareID string `json:"shareID" gorm:"index"` // 參考 TripShare.ID

	// 動作類型
	Action string `json:"action"` // "created" | "accepted" | "declined" | "copied" | "revoked" | "expired"

	// 執行者
	ActorID string `json:"actorID" gorm:"index"`

	// 動作詳情（JSON）
	Details map[string]any `json:"details,omitempty" gorm:"serializer:json"`

	CreatedAt time.Time `json:"createdAt"`
}
