// Package model - Trip Sharing 數據結構定義
package model

import "time"

// TripShare 是行程分享記錄
// 用戶可以將自己頻道的行程分享給其他人或複製到其他頻道
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
	ShareToken string `json:"shareToken,omitempty" gorm:"uniqueIndex"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"` // 連結過期時間 (null = 永不過期)

	// 分享狀態
	Status     string `json:"status"` // "pending" | "accepted" | "declined" | "active"
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	AcceptedBy string `json:"acceptedBy,omitempty"` // 實際接受的使用者

	// 複製結果：接收者複製到自己頻道後的新 Trip
	DestinationTripID    string `json:"destinationTripID,omitempty"`
	DestinationChannelID string `json:"destinationChannelID,omitempty"`

	// 中繼資料
	Message   string `json:"message,omitempty"` // 分享備註
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TripShareHistory 是分享審計日誌
// 記錄每個分享的所有操作歷史
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

// TripShareStats - 分享統計（用於推薦）
type TripShareStats struct {
	TripID          string `json:"tripID"`
	TotalShares     int    `json:"totalShares"`
	TotalCopies     int    `json:"totalCopies"`
	LastSharedAt    *time.Time `json:"lastSharedAt"`
	MostSharedBy    string `json:"mostSharedBy,omitempty"`
}

// ShareNotification - 分享通知（用於發送給接收者）
type ShareNotification struct {
	ID        string `json:"id"`
	ShareID   string `json:"shareID"`
	UserID    string `json:"userID"`
	Read      bool   `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}
