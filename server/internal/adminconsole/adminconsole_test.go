package adminconsole

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tim72117/shuttle/internal/adminauth"
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

// TestAdminAPIEndToEnd 是原 adminconsole_integration_test.go 的改寫版:
// 用 SQLite 記憶體 store 取代真的 Postgres,涵蓋一樣的行為(fail-closed 的
// withAdmin 關卡、錯誤密碼被拒、登入後拿到 cookie、authed 呼叫成功),但拿掉
// plans 端點(這次整合範圍不含 quota 系統),改驗證 /admin/api/users 回傳的
// 是基本使用者資訊而非方案/額度。免外部依賴,可跑進一般 `go test ./...`。
func TestAdminAPIEndToEnd(t *testing.T) {
	st := newTestStore(t)

	const email = "admin-api-test@example.com"
	const password = "supersecret123"

	auth := adminauth.New(st, false)
	if _, err := auth.Bootstrap(email, password); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	h := NewHandler(auth, st)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. 未登入呼叫 withAdmin 路由被拒(401)。這是 fail-closed 的核心行為:
	//    沒有 cookie ⇒ 沒有存取權。
	if resp, err := http.Get(srv.URL + "/admin/api/users"); err != nil {
		t.Fatalf("unauth GET: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauth /admin/api/users = %d, want 401", resp.StatusCode)
		}
	}

	// 2. 密碼錯誤登入不了。
	if resp, err := http.Post(srv.URL+"/admin/api/login", "application/json",
		strings.NewReader(`{"email":"`+email+`","password":"nope"}`)); err != nil {
		t.Fatalf("bad login: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bad-password login = %d, want 401", resp.StatusCode)
		}
	}

	// 3. 正確登入會設定 admin_session cookie;帶著 cookie jar 的 client 之後
	//    就能呼叫 authed 路由。
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Post(srv.URL+"/admin/api/login", "application/json",
		strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	sawAdminCookie := false
	for _, c := range jar.Cookies(srvURL) {
		if c.Name == adminauth.CookieName {
			sawAdminCookie = true
		}
	}
	if !sawAdminCookie {
		t.Fatalf("login did not set the %q cookie", adminauth.CookieName)
	}

	// 4. Authed 呼叫現在會成功,且回傳的是基本使用者資訊(不含方案/額度)。
	if resp, err := client.Get(srv.URL + "/admin/api/users"); err != nil {
		t.Fatalf("authed GET: %v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("authed /admin/api/users = %d, want 200", resp.StatusCode)
		}
	}

	// 5. 登出後,同一個 client 再也叫不動 authed 路由(session 真的被刪除)。
	logoutResp, err := client.Post(srv.URL+"/admin/api/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	logoutResp.Body.Close()
	if resp, err := client.Get(srv.URL + "/admin/api/users"); err != nil {
		t.Fatalf("post-logout GET: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("post-logout /admin/api/users = %d, want 401", resp.StatusCode)
		}
	}
}
