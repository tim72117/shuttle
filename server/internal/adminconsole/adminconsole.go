// Package adminconsole 提供管理後台 API,掛載於 /admin/api/*。刻意是與
// internal/api(一般使用者/前端用的 API)分開的獨立 handler:獨立身分系統
// (internal/adminauth)、獨立 cookie、自己的 withAdmin 關卡(fail-closed:
// 非通過驗證的管理員一律拒絕)。這次整合範圍只有「管理員登入」+「使用者
// 列表查詢」——不含方案(plan)/額度(quota)/用量追蹤,那些功能明確排除在外。
package adminconsole

import (
	"encoding/json"
	"net/http"

	"github.com/tim72117/shuttle/internal/adminauth"
	"github.com/tim72117/shuttle/internal/model"
	"github.com/tim72117/shuttle/internal/store"
)

// Handler serves /admin/api/*. Auth 是管理員身分/登入態存取層;Store 提供
// 使用者清單查詢。
type Handler struct {
	Auth  *adminauth.Store
	Store *store.Store
}

func NewHandler(auth *adminauth.Store, st *store.Store) *Handler {
	return &Handler{Auth: auth, Store: st}
}

// Register mounts the admin API routes on mux. login 不需要先登入(這是「變成
// 已登入」的手段);其餘都掛在 withAdmin 之後。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/api/login", h.login)
	mux.HandleFunc("POST /admin/api/logout", h.logout)
	mux.HandleFunc("GET /admin/api/me", h.withAdmin(h.me))
	mux.HandleFunc("GET /admin/api/users", h.withAdmin(h.listUsers))
}

// withAdmin 是每一條特權管理路由共用的關卡:解析管理員 session cookie,
// 解析不出合法管理員就回 401。由建構方式保證 fail-closed——包在 withAdmin
// 裡的 handler 不可能被非管理員執行到。
func (h *Handler) withAdmin(next func(http.ResponseWriter, *http.Request, *adminauth.Admin)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, ok := h.Auth.Verify(r)
		if !ok {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		next(w, r, admin)
	}
}

// --- auth ----------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type adminResponse struct {
	Email string `json:"email"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	admin, err := h.Auth.Login(req.Email, req.Password)
	if err != nil {
		// email 不存在與密碼錯誤共用同一個籠統訊息(見 adminauth.ErrInvalidCredentials)。
		http.Error(w, "invalid admin credentials", http.StatusUnauthorized)
		return
	}
	if _, err := h.Auth.CreateSession(w, admin.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, adminResponse{Email: admin.Email})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.Auth.Logout(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request, admin *adminauth.Admin) {
	writeJSON(w, http.StatusOK, adminResponse{Email: admin.Email})
}

// --- users -----------------------------------------------------------------

type usersResponse struct {
	Total int                      `json:"total"`
	Users []model.AdminUserSummary `json:"users"`
}

// listUsers 回傳一般使用者清單(基本身分資訊——id/email/name/大頭貼顏色)。
// 刻意不含方案/額度/用量,這次整合範圍不含 quota 系統。
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request, _ *adminauth.Admin) {
	users, err := h.Store.ListUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, usersResponse{Total: len(users), Users: users})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
