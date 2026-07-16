package store

import "time"

// 以下 entity 是 GORM 的資料表映射(帶 gorm tag),與 API DTO(model.*)分離。
// store 方法負責 entity <-> model 的轉換。

type userRow struct {
	ID           string  `gorm:"primaryKey;column:id"`
	Name         string  `gorm:"column:name;not null"`
	AvatarColor  string  `gorm:"column:avatar_color;not null"`
	AppleSub     *string `gorm:"column:apple_sub;uniqueIndex"` // 可為 NULL
	Email        *string `gorm:"column:email;uniqueIndex"`     // 可為 NULL
	PasswordHash *string `gorm:"column:password_hash"`         // 可為 NULL

	// 多對多:此使用者參與的頻道(透過 members 中介表)。
	Channels []channelRow `gorm:"many2many:members;joinForeignKey:user_id;joinReferences:channel_id"`
}

func (userRow) TableName() string { return "users" }

type channelRow struct {
	ID        string    `gorm:"primaryKey;column:id"`
	Name      string    `gorm:"column:name;not null"`
	OwnerID   string    `gorm:"column:owner_id;not null;default:''"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`

	// 多對多:頻道成員(透過 members 中介表)。
	Members []userRow `gorm:"many2many:members;joinForeignKey:channel_id;joinReferences:user_id"`
}

func (channelRow) TableName() string { return "channels" }

// entryRow 是主體:LLM 處理使用者輸入後產出的「事件/條目」。
// 承載所有 LLM 結構化結果——事件時間(title/start/end/allDay)與標注(category/tags/note)。
// 原話(message)不存後端,改由各裝置端 DB 保存(local-first)。
type entryRow struct {
	ID        string   `gorm:"primaryKey;column:id"`
	ChannelID string   `gorm:"column:channel_id;not null;index"`
	Title     string   `gorm:"column:title;not null"`
	Start     string   `gorm:"column:start"`
	StartTime string   `gorm:"column:start_time"` // 'HH:MM';空=全日
	End       string   `gorm:"column:end_at"`     // end 是 SQL 保留字,欄位改名 end_at
	EndTime   string   `gorm:"column:end_time"`   // 'HH:MM'
	Location  string   `gorm:"column:location"`
	Lat       *float64 `gorm:"column:lat"`
	Lng       *float64 `gorm:"column:lng"`
	// 所屬行程;NULL=未歸組。後端依時間自動歸組。
	TripID *string `gorm:"column:trip_id;index"`
	// LLM 標注(原本在 message 上,改存 entry)。
	Category  *string        `gorm:"column:category"`
	Tags      []string       `gorm:"column:tags;serializer:json"`
	Note      *string        `gorm:"column:note"`
	Kind      *string        `gorm:"column:kind"`
	Detail    map[string]any `gorm:"column:detail;serializer:json"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
}

func (entryRow) TableName() string { return "entries" }

// tripRow 是 entries 的行程分組(對齊 entryRow 的字串時間慣例)。
type tripRow struct {
	ID        string    `gorm:"primaryKey;column:id"`
	ChannelID string    `gorm:"column:channel_id;not null;index"`
	Title     string    `gorm:"column:title"`
	Start     string    `gorm:"column:start"`
	End       string    `gorm:"column:end_at"` // end 是 SQL 保留字,對齊 entryRow 改名 end_at
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (tripRow) TableName() string { return "trips" }

// publicLinkRow 是頻道公開分享連結，一個頻道最多一條。
type publicLinkRow struct {
	ID        string    `gorm:"primaryKey;column:id"`
	ChannelID string    `gorm:"uniqueIndex;column:channel_id;not null"`
	LinkToken string    `gorm:"uniqueIndex;column:link_token;not null"`
	CreatedBy string    `gorm:"column:created_by;not null"`
	Editable  bool      `gorm:"column:editable;not null;default:false"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (publicLinkRow) TableName() string { return "public_links" }
