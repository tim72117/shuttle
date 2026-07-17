package adminauth

import (
	"net/http/httptest"
	"testing"

	"github.com/tim72117/shuttle/internal/store"
)

// newTestStore 用 SQLite 記憶體 DB 建一個乾淨的 store,對齊
// internal/store 既有測試的慣例(store/trips_test.go 的 newTestStore)。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestBootstrapAndLogin 涵蓋原 adminauth_integration_test.go 的行為,改用
// shuttle 的 GORM store(SQLite 記憶體)取代原本連真的 Postgres 的整合測試,
// 免外部依賴、可跑進一般 `go test ./...`。
func TestBootstrapAndLogin(t *testing.T) {
	st := newTestStore(t)
	auth := New(st, false)

	const email = "admin-test@example.com"
	const password = "supersecret123"

	// 空白設定是 no-op(沒有要求 Bootstrap)。
	if created, err := auth.Bootstrap("", ""); err != nil || created {
		t.Fatalf("blank bootstrap: created=%v err=%v, want false/nil", created, err)
	}

	// 第一次真正的 Bootstrap 會建立管理員。
	created, err := auth.Bootstrap(email, password)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !created {
		t.Fatal("first bootstrap should have created the admin")
	}
	if n := auth.Count(); n != 1 {
		t.Fatalf("Count() = %d, want 1", n)
	}

	// 冪等:同一 email 再跑一次 Bootstrap,不會重複建立,也不會出錯
	// (每次啟動都安全執行)。
	created2, err := auth.Bootstrap(email, password)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if created2 {
		t.Fatal("second bootstrap must not create a duplicate admin")
	}

	// 正確密碼可登入。
	admin, err := auth.Login(email, password)
	if err != nil {
		t.Fatalf("login with correct password failed: %v", err)
	}
	if admin.Email != email {
		t.Errorf("logged-in admin email = %q, want %q", admin.Email, email)
	}
	if admin.ID == "" {
		t.Error("logged-in admin ID is empty")
	}

	// 密碼錯誤一律回傳同一個籠統錯誤(不讓探測者分辨「email 不存在」與
	// 「密碼錯誤」)。
	if _, err := auth.Login(email, "wrong-password"); err != ErrInvalidCredentials {
		t.Errorf("wrong password: got %v, want ErrInvalidCredentials", err)
	}

	// 未知 email 回傳相同的錯誤。
	if _, err := auth.Login("nobody@example.com", password); err != ErrInvalidCredentials {
		t.Errorf("unknown email: got %v, want ErrInvalidCredentials", err)
	}

	// Bootstrap 不可以重設既有管理員的密碼:用不同密碼再 Bootstrap 一次之後,
	// 原密碼仍可登入,新密碼不行。這是「每次啟動都安全執行」承諾的關鍵——
	// 重啟時若環境變數的密碼被改了,不能悄悄洗掉之後用其他方式改過的密碼。
	if _, err := auth.Bootstrap(email, "a-different-password-9999"); err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}
	if _, err := auth.Login(email, password); err != nil {
		t.Errorf("original password stopped working after re-bootstrap: %v", err)
	}
	if _, err := auth.Login(email, "a-different-password-9999"); err != ErrInvalidCredentials {
		t.Errorf("re-bootstrap wrongly changed the password")
	}
}

// TestSessionLifecycle 驗證 CreateSession → Verify → Logout 的完整循環,
// 特別是登出後 session 記錄真的從資料庫消失(DELETE,不是只清 cookie)。
func TestSessionLifecycle(t *testing.T) {
	st := newTestStore(t)
	auth := New(st, false)

	const email = "session-test@example.com"
	const password = "supersecret123"
	if _, err := auth.Bootstrap(email, password); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, err := auth.Login(email, password)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// 建立 session,拿到 Set-Cookie。
	rec := httptest.NewRecorder()
	sessionID, err := auth.CreateSession(rec, admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sessionID == "" {
		t.Fatal("session id is empty")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName {
		t.Fatalf("expected one %q cookie, got %+v", CookieName, cookies)
	}

	// 帶著這個 cookie 的請求可以通過 Verify。
	req := httptest.NewRequest("GET", "/admin/api/me", nil)
	req.AddCookie(cookies[0])
	got, ok := auth.Verify(req)
	if !ok {
		t.Fatal("Verify() with a fresh session cookie should succeed")
	}
	if got.Email != email {
		t.Errorf("Verify() email = %q, want %q", got.Email, email)
	}

	// 沒有 cookie 的請求 fail-closed 拒絕。
	noCookieReq := httptest.NewRequest("GET", "/admin/api/me", nil)
	if _, ok := auth.Verify(noCookieReq); ok {
		t.Fatal("Verify() with no cookie should fail")
	}

	// Logout 之後,資料庫裡的 session 真的被刪除:同一張 cookie 再也
	// Verify 不過。
	logoutRec := httptest.NewRecorder()
	auth.Logout(logoutRec, req)
	if _, ok := auth.Verify(req); ok {
		t.Fatal("Verify() should fail after Logout deleted the session")
	}
}
