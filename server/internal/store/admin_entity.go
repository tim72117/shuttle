package store

import "time"

// adminUserRow 是管理員帳號(對照 adminauth 的 Admin),與一般使用者(userRow)
// 完全分離的資料表:沒有註冊 API,只能靠 Bootstrap(啟動時讀環境變數)產生。
// ID 比照 shuttle 慣例採字串(如 adm_xxx),不用原始參考版本的 BIGSERIAL。
type adminUserRow struct {
	ID           string    `gorm:"primaryKey;column:id"`
	Email        string    `gorm:"column:email;not null;uniqueIndex"`
	PasswordHash string    `gorm:"column:password_hash;not null"` // bcrypt
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

func (adminUserRow) TableName() string { return "admin_users" }

// adminSessionRow 是管理員登入態(DB-backed cookie session),與一般使用者的
// 無狀態 JWT 系統完全分離。查無或過期一律視為未授權(fail-closed),登出時
// 直接刪除對應資料列。
type adminSessionRow struct {
	ID          string    `gorm:"primaryKey;column:id"`
	AdminUserID string    `gorm:"column:admin_user_id;not null;index"`
	ExpiresAt   time.Time `gorm:"column:expires_at;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (adminSessionRow) TableName() string { return "admin_sessions" }
