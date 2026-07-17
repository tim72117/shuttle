package store

import (
	"errors"
	"time"

	"github.com/tim72117/shuttle/internal/model"
	"gorm.io/gorm"
)

// AdminAccount 是管理員帳號的 store 層 DTO(entity <-> 呼叫端轉換),
// 與一般使用者(model.User)完全分離,對齊 adminauth 的獨立身分系統設計。
type AdminAccount struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

func toAdminAccount(r adminUserRow) AdminAccount {
	return AdminAccount{ID: r.ID, Email: r.Email, PasswordHash: r.PasswordHash, CreatedAt: r.CreatedAt}
}

// CreateAdminUser 建立一個管理員帳號。email 須唯一(衝突時回傳 error)。
// 僅供 adminauth.Bootstrap 呼叫——沒有對外的註冊 API。
func (s *Store) CreateAdminUser(id, email, passwordHash string) (AdminAccount, error) {
	r := adminUserRow{ID: id, Email: email, PasswordHash: passwordHash, CreatedAt: now()}
	if err := s.db.Create(&r).Error; err != nil {
		return AdminAccount{}, err
	}
	return toAdminAccount(r), nil
}

// FindAdminByEmail 依 email(大小寫不敏感)查管理員帳號,找不到回傳 ErrNotFound。
func (s *Store) FindAdminByEmail(email string) (AdminAccount, error) {
	var r adminUserRow
	err := s.db.Where("lower(email) = lower(?)", email).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AdminAccount{}, ErrNotFound
	}
	if err != nil {
		return AdminAccount{}, err
	}
	return toAdminAccount(r), nil
}

// CountAdminUsers 回傳管理員帳號總數(啟動時記錄用)。
func (s *Store) CountAdminUsers() (int64, error) {
	var n int64
	if err := s.db.Model(&adminUserRow{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CreateAdminSession 建立一筆管理員登入態(DB-backed session),expiresAt 由呼叫端
// (adminauth)依 TTL 算好傳入。
func (s *Store) CreateAdminSession(id, adminUserID string, expiresAt time.Time) error {
	r := adminSessionRow{ID: id, AdminUserID: adminUserID, ExpiresAt: expiresAt, CreatedAt: now()}
	return s.db.Create(&r).Error
}

// FindAdminSessionWithUser 依 session id 查對應的管理員帳號,並檢查是否過期。
// 查無 session、session 已過期、或管理員帳號已不存在,一律回傳 ErrNotFound
// (fail-closed:呼叫端 adminauth.Verify 把任何錯誤都視為未授權)。
func (s *Store) FindAdminSessionWithUser(sessionID string) (AdminAccount, error) {
	var sess adminSessionRow
	err := s.db.Where("id = ?", sessionID).First(&sess).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AdminAccount{}, ErrNotFound
	}
	if err != nil {
		return AdminAccount{}, err
	}
	if !sess.ExpiresAt.After(now()) {
		return AdminAccount{}, ErrNotFound
	}

	var admin adminUserRow
	err = s.db.Where("id = ?", sess.AdminUserID).First(&admin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AdminAccount{}, ErrNotFound
	}
	if err != nil {
		return AdminAccount{}, err
	}
	return toAdminAccount(admin), nil
}

// DeleteAdminSession 刪除一筆管理員 session(登出用)。查無此 session 不算錯誤
// (冪等——重複登出、或 cookie 帶著已過期/已被清掉的 id 都安全)。
func (s *Store) DeleteAdminSession(sessionID string) error {
	return s.db.Where("id = ?", sessionID).Delete(&adminSessionRow{}).Error
}

// ListUsers 列出一般使用者清單,供 /admin/api/users 使用。刻意只回傳基本資訊
// (id/email/name/大頭貼顏色),不含方案/額度/用量——那些功能不在這次整合範圍內。
// userRow 目前沒有 CreatedAt 欄位,故不虛構「建立時間」這個回傳欄位。
func (s *Store) ListUsers() ([]model.AdminUserSummary, error) {
	var rows []userRow
	if err := s.db.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.AdminUserSummary, 0, len(rows))
	for _, r := range rows {
		email := ""
		if r.Email != nil {
			email = *r.Email
		}
		out = append(out, model.AdminUserSummary{
			ID:          r.ID,
			Email:       email,
			Name:        r.Name,
			AvatarColor: r.AvatarColor,
		})
	}
	return out, nil
}
