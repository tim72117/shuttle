// Package adminauth 是管理後台(adminconsole,掛載於 /admin)的身分層。
// 刻意與一般使用者系統(internal/auth + JWT)完全分離:獨立的資料表
// (admin_users/admin_sessions,透過 store.Store 存取)、獨立的 cookie
// (admin_session)、DB-backed session(非無狀態 JWT)。管理員不是「一般使用者
// 加一個角色欄位」——一般使用者流程的任何弱點都不會外溢成管理員權限,反之
// 持有一般使用者登入態也不代表擁有這裡的任何存取權。
//
// 機制(bcrypt、DB-backed opaque session id、httpOnly+Secure cookie、依部署
// 環境決定的 SameSite)刻意比照一般使用者的 session 設計:相同的强化手段,
// 不同的身分領域。這個套件多出的是 Bootstrap——沒有自助註冊,第一個管理員
// 帳號由環境變數在啟動時建立。
package adminauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tim72117/shuttle/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// CookieName 是管理員 session 的 cookie 名稱。刻意與一般使用者系統的 JWT
// 無關(那套系統不用 cookie),獨立命名避免混淆。
const CookieName = "admin_session"

// sessionTTL 比一般使用者的登入態短:管理權限較高,故較快要求重新登入。
const sessionTTL = 12 * time.Hour

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ErrInvalidCredentials 涵蓋「email 不存在」與「密碼錯誤」兩種情況,刻意不
// 區分(避免探測帳號是否存在)。
var ErrInvalidCredentials = errors.New("invalid admin credentials")

// Admin 是通過驗證的管理員身分。
type Admin struct {
	ID    string
	Email string
}

// Store 是管理員身分/登入態的存取層,底層透過 shuttle 既有的 store.Store
// (GORM)操作 admin_users / admin_sessions 兩張表。
type Store struct {
	db *store.Store
	// Secure 控制 session cookie 的 Secure 屬性:正式(HTTPS)環境應為 true,
	// 僅本機 HTTP 開發時為 false。
	Secure bool
}

// New 建立一個 adminauth.Store,包著 shuttle 的 store.Store。
func New(db *store.Store, secure bool) *Store {
	return &Store{db: db, Secure: secure}
}

// Bootstrap 確保給定 email 的管理員帳號存在,不存在就用給定密碼建立。這是
// 「第一個管理員」唯一的誕生方式——沒有註冊 API,信任根是部署環境(誰能設定
// ADMIN_BOOTSTRAP_* 環境變數),而不是任何 API 呼叫者。冪等,每次啟動執行都
// 安全:email 已存在就不動它(不重設密碼,避免之後手動改過的密碼被開機洗掉)。
//
// 回傳是否真的建立了新帳號(供記錄用)。email 或 password 任一為空,視為
// 「沒有設定 Bootstrap」,回傳 (false, nil),不觸碰資料庫。
func (s *Store) Bootstrap(email, password string) (created bool, err error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return false, nil
	}
	if !emailRE.MatchString(email) {
		return false, fmt.Errorf("adminauth: bootstrap email is not a valid address")
	}
	if len(password) < 8 {
		return false, fmt.Errorf("adminauth: bootstrap password must be at least 8 characters")
	}

	if _, err := s.db.FindAdminByEmail(email); err == nil {
		return false, nil // 已存在——不動它
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, fmt.Errorf("adminauth: check existing admin: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("adminauth: hash bootstrap password: %w", err)
	}
	id := "adm_" + randHex()
	if _, err := s.db.CreateAdminUser(id, email, string(hash)); err != nil {
		// 併發啟動多個實例時可能撞到 unique index(email 已被搶先建立);
		// 這種情況視為「已存在」,不算錯誤(輸的一方直接 no-op)。
		if _, findErr := s.db.FindAdminByEmail(email); findErr == nil {
			return false, nil
		}
		return false, fmt.Errorf("adminauth: insert bootstrap admin: %w", err)
	}
	return true, nil
}

// Login 驗證管理員的 email/密碼。ErrInvalidCredentials 同時涵蓋「email 不
// 存在」與「密碼錯誤」。
func (s *Store) Login(email, password string) (*Admin, error) {
	acc, err := s.db.FindAdminByEmail(email)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("adminauth: query admin: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return &Admin{ID: acc.ID, Email: acc.Email}, nil
}

// CreateSession 為 adminID 建立一筆管理員 session,並把 cookie 設到 w 上。
func (s *Store) CreateSession(w http.ResponseWriter, adminID string) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", fmt.Errorf("adminauth: generate id: %w", err)
	}
	expiresAt := time.Now().Add(sessionTTL)

	if err := s.db.CreateAdminSession(id, adminID, expiresAt); err != nil {
		return "", fmt.Errorf("adminauth: insert session: %w", err)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: sameSite(s.Secure),
	})
	return id, nil
}

// Verify 解析 r 上的管理員 session cookie。cookie 不存在、session 查無/過期、
// 或對應的管理員帳號已不存在,ok 一律為 false(fail-closed)。
func (s *Store) Verify(r *http.Request) (admin *Admin, ok bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return nil, false
	}
	acc, err := s.db.FindAdminSessionWithUser(cookie.Value)
	if err != nil {
		return nil, false
	}
	return &Admin{ID: acc.ID, Email: acc.Email}, true
}

// Logout 刪除 r 的 cookie 所指的管理員 session(資料庫真的刪除該筆記錄),並
// 清掉 w 上的 cookie。
func (s *Store) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(CookieName); err == nil {
		_ = s.db.DeleteAdminSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: sameSite(s.Secure),
	})
}

// Count 回傳目前管理員帳號總數(啟動時記錄用)。
func (s *Store) Count() int {
	n, err := s.db.CountAdminUsers()
	if err != nil {
		return 0
	}
	return int(n)
}

// sameSite 比照一般使用者 session 的邏輯:正式 HTTPS 部署用 None(要求
// Secure)——管理 SPA 與後端可能跨站;本機 HTTP 開發用 Lax,同站 fetch 瀏覽器
// 仍會送出。
func sameSite(secure bool) http.SameSite {
	if secure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func randomID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// randHex 產生管理員帳號 ID 的隨機後綴,比照 shuttle 既有的 ID 產生慣例
// (server/cmd/server/helpers.go 的 randHex、server/internal/api/api.go 的
// newID:皆為 4 bytes 隨機值轉 hex)。adminauth 是獨立套件,不便直接呼叫那兩個
// unexported 函式,故在此照同樣邏輯本地實作一份,不引入不同的 ID 產生方式。
func randHex() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}
