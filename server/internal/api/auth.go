package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tim72117/shuttle/internal/auth"
	"github.com/tim72117/shuttle/internal/model"
	"github.com/tim72117/shuttle/internal/store"
)

// userFor 從請求的 Authorization header 解析目前使用者。
// 無 token 或 token 無效時,回退為訪客(維持可跳過登入的體驗)。
func (s *Server) userFor(r *http.Request) model.User {
	token, err := auth.ParseBearer(r.Header.Get("Authorization"))
	if err != nil {
		return s.guestUser
	}
	return s.userFromToken(token)
}

// userFromToken 驗證 JWT 字串並解析出使用者;token 無效時回訪客。
// 供 userFor(header 取 token)與 WS 升級(query string 取 token,瀏覽器原生
// WebSocket API 不支援自訂 header)共用同一套驗證邏輯。
func (s *Server) userFromToken(token string) model.User {
	claims, err := s.signer.Verify(token)
	if err != nil {
		return s.guestUser
	}
	// 以 token 內的身分為主;若 DB 查得到使用者就用最新資料。
	if u, err := s.store.FindUserByID(claims.Sub); err == nil {
		return u
	}
	return model.User{ID: claims.Sub, Name: claims.Name, AvatarColor: "#8C7B6A"}
}

// POST /v1/auth/apple
// Body: { "identityToken": "...", "fullName": "可選,首次登入時帶入" }
// 驗證 Apple identity token → 建立或查出使用者 → 簽自家 JWT 回傳。
func (s *Server) handleAppleAuth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdentityToken string `json:"identityToken"`
		FullName      string `json:"fullName"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.IdentityToken == "" {
		writeErr(w, http.StatusBadRequest, "missing_token", "identityToken 不可為空")
		return
	}

	identity, err := auth.VerifyAppleToken(body.IdentityToken, s.devMode)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "apple_verify_failed", err.Error())
		return
	}

	// 依 Apple sub 找使用者,沒有就建立。
	user, err := s.store.FindUserByAppleSub(identity.Sub)
	if errors.Is(err, store.ErrNotFound) {
		name := body.FullName
		if name == "" {
			name = "Apple 使用者"
		}
		user, err = s.store.CreateAppleUser("usr_"+newID(), name, "#8C7B6A", identity.Sub)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "user_failed", err.Error())
		return
	}

	s.issueToken(w, user, identity.Email)
}

// POST /v1/auth/register
// Body: { "email", "password", "name" }
// 建立帳密使用者 → 簽自家 JWT 回傳(註冊即登入)。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" || body.Password == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "email 與 password 不可為空")
		return
	}
	if len(body.Password) < 6 {
		writeErr(w, http.StatusBadRequest, "weak_password", "密碼至少 6 字元")
		return
	}
	// email 已存在?
	if _, _, err := s.store.FindUserByEmail(email); err == nil {
		writeErr(w, http.StatusConflict, "email_taken", "此 email 已被註冊")
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash_failed", err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = email
	}
	user, err := s.store.CreatePasswordUser("usr_"+newID(), name, "#8C7B6A", email, hash)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	s.issueToken(w, user, email)
}

// POST /v1/auth/login
// Body: { "email", "password" }
// 驗證帳密 → 簽自家 JWT 回傳。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	user, hash, err := s.store.FindUserByEmail(email)
	// 帳號不存在或密碼錯誤,一律回相同錯誤(不洩漏帳號是否存在)。
	if err != nil || hash == "" || auth.VerifyPassword(body.Password, hash) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "email 或密碼錯誤")
		return
	}
	s.issueToken(w, user, email)
}

// issueToken 簽發 JWT 並以 { token, user, profile } 回應(register/login/apple 共用)。
// user 為公開身分;profile 含私密資料(email),分離設計。
func (s *Server) issueToken(w http.ResponseWriter, user model.User, email string) {
	token, err := s.signer.Sign(user.ID, user.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sign_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"user":    user,
		"profile": model.Profile{Email: email},
	})
}

// GET /v1/me — 用 Bearer token 取得目前登入使用者(驗證 token 是否仍有效)。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	token, err := auth.ParseBearer(r.Header.Get("Authorization"))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "no_token", "缺少 Authorization")
		return
	}
	claims, err := s.signer.Verify(token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid_token", "token 無效或過期")
		return
	}
	user, err := s.store.FindUserByID(claims.Sub)
	if err != nil {
		// token 有效但使用者已不存在
		user = model.User{ID: claims.Sub, Name: claims.Name, AvatarColor: "#8C7B6A"}
	}
	email, _ := s.store.GetUserEmail(claims.Sub)
	writeJSON(w, http.StatusOK, model.Me{
		User:    user,
		Profile: model.Profile{Email: email},
	})
}
