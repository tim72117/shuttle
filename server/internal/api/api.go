// Package api 提供對齊 docs/API.md 的 HTTP handlers。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/channel/server/internal/auth"
	"github.com/channel/server/internal/llm"
	"github.com/channel/server/internal/model"
	"github.com/channel/server/internal/store"
)

type Server struct {
	store    *store.Store
	analyzer llm.Analyzer
	signer   *auth.Signer
	hub      *Hub
	// devMode:Apple token 不驗簽章(原型用)。
	devMode bool
	// 未登入時的預設使用者(維持可跳過登入的體驗)。
	guestUser model.User
}

func New(st *store.Store, an llm.Analyzer, signer *auth.Signer, devMode bool) *Server {
	return &Server{
		store:     st,
		analyzer:  an,
		signer:    signer,
		hub:       newHub(),
		devMode:   devMode,
		guestUser: model.User{ID: "usr_me", Name: "我", AvatarColor: "#4A90D9"},
	}
}

// NotifyEntriesUpdated 廣播 entries_updated 給指定頻道的訂閱者(供 wanttools 呼叫)。
func (s *Server) NotifyEntriesUpdated(channelID string) {
	s.hub.Broadcast(channelID, map[string]any{"event": "entries_updated", "channelID": channelID})
}

// Routes 註冊路由(Go 1.22+ 的方法+路徑樣式)。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /v1/auth/apple", s.handleAppleAuth)
	mux.HandleFunc("POST /v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("GET /v1/me", s.handleMe)
	mux.HandleFunc("GET /v1/channels", s.handleListChannels)
	mux.HandleFunc("POST /v1/channels", s.handleCreateChannel)
	mux.HandleFunc("GET /v1/channels/{id}/members", s.handleListMembers)
	mux.HandleFunc("POST /v1/channels/{id}/members", s.handleAddMember)
	mux.HandleFunc("PATCH /v1/channels/{id}/members/{userID}", s.handleSetMemberRole)
	mux.HandleFunc("POST /v1/channels/{id}/query", s.handleQuery)
	mux.HandleFunc("POST /v1/channels/{id}/assist", s.handleAssist)
	mux.HandleFunc("GET /v1/channels/{id}/entries", s.handleListEntries)
	mux.HandleFunc("DELETE /v1/channels/{id}/entries", s.handleResetChannelData)
	mux.HandleFunc("GET /v1/channels/{id}/trips", s.handleListTrips)
	mux.HandleFunc("GET /v1/channels/{id}/trips/{tripID}/entries", s.handleListTripEntries)
	mux.HandleFunc("GET /v1/channels/{id}/ws", s.handleWS)
	mux.HandleFunc("POST /v1/channels/{id}/public-link", s.handleCreatePublicLink)
	mux.HandleFunc("GET /v1/channels/{id}/public-link", s.handleGetPublicLink)
	mux.HandleFunc("DELETE /v1/channels/{id}/public-link", s.handleDeletePublicLink)
	mux.HandleFunc("GET /v1/public/{token}", s.handlePublicView)
	// internal — 供 CLI / LLM 操作資料，不需登入
	mux.HandleFunc("GET /internal/channels", s.handleInternalListChannels)
	mux.HandleFunc("POST /internal/channels/{id}/notify", s.handleNotify)
	mux.HandleFunc("POST /internal/channels/{id}/entries", s.handleInternalRecord)
	mux.HandleFunc("POST /internal/entries/{id}/trip", s.handleInternalAddToTrip)
	mux.HandleFunc("PATCH /internal/entries/{id}", s.handleInternalUpdateEntry)
	mux.HandleFunc("PATCH /internal/entries/{id}/latlng", s.handleInternalSetLatLng)
	mux.HandleFunc("GET /internal/channels/{id}/trips", s.handleInternalListTrips)
	mux.HandleFunc("GET /internal/channels/{id}/trips/{tripID}/entries", s.handleInternalTripEntries)
	mux.HandleFunc("DELETE /internal/channels/{id}/entries", s.handleInternalReset)
	return logging(cors(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /v1/channels — 只回目前使用者參與的頻道。
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	user := s.userFor(r)
	chs, err := s.store.ListChannelsForUser(user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if chs == nil {
		chs = []model.Channel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": chs})
}

// POST /v1/channels  { "name": "..." }
func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_name", "name 不可為空")
		return
	}
	ch, err := s.store.CreateChannel("ch_"+newID(), body.Name, s.userFor(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

// 原話(message)已移至各裝置端 DB,後端不再保存或提供 messages 端點。
// owner 記事走 POST /assist(LLM 解析成 entry),member 查詢走 POST /query(查 entry)。

// GET /v1/channels/{id}/members
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	members, err := s.store.ListMembers(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if members == nil {
		members = []model.Member{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// POST /v1/channels/{id}/members  { "email": "...", "role": "editor"|"viewer" }
// 以 email 查出使用者後加入頻道。role 留空預設 viewer。僅 owner 能加入成員。
func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := s.userFor(r)

	// 權限:只有 owner 能加入/管理成員。
	if !s.requireOwner(w, id, user.ID) {
		return
	}

	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" {
		writeErr(w, http.StatusBadRequest, "invalid_email", "email 不可為空")
		return
	}
	role := body.Role
	if role != model.RoleEditor && role != model.RoleViewer {
		role = model.RoleViewer // 預設或非法值一律 viewer
	}

	u, _, err := s.store.FindUserByEmail(email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "user_not_found", "找不到使用此 email 的使用者")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}

	members, err := s.store.AddMember(id, u, role)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "channel_not_found", "頻道不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "add_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// PATCH /v1/channels/{id}/members/{userID}  { "role": "editor"|"viewer" }
// 變更成員角色。僅 owner 能改;不能改 owner 自己的角色(owner 恆為 editor)。
func (s *Server) handleSetMemberRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	targetID := r.PathValue("userID")
	user := s.userFor(r)

	if !s.requireOwner(w, id, user.ID) {
		return
	}
	if targetID == user.ID {
		writeErr(w, http.StatusBadRequest, "cannot_change_owner", "不能變更頻道擁有者自己的角色")
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Role != model.RoleEditor && body.Role != model.RoleViewer {
		writeErr(w, http.StatusBadRequest, "invalid_role", "role 須為 editor 或 viewer")
		return
	}

	if err := s.store.SetMemberRole(id, targetID, body.Role); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "member_not_found", "該成員不在此頻道")
			return
		}
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	members, err := s.store.ListMembers(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// requireOwner 檢查 userID 是否為頻道 owner;非 owner 時寫入錯誤回應並回 false。
func (s *Server) requireOwner(w http.ResponseWriter, channelID, userID string) bool {
	owner, err := s.store.GetChannelOwner(channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "channel_not_found", "頻道不存在")
			return false
		}
		writeErr(w, http.StatusInternalServerError, "owner_check_failed", err.Error())
		return false
	}
	if userID != owner {
		writeErr(w, http.StatusForbidden, "not_owner", "只有頻道擁有者能管理成員")
		return false
	}
	return true
}

// requireEditor 檢查 userID 在頻道內是否有 editor 角色(可修改/記事);
// 非成員或非 editor 時寫入錯誤回應並回 false。
// owner 恆視為 editor:不論 members.role 為何(例如後補欄位時被預設成 viewer),
// owner 一律放行,確保頻道擁有者永遠能記事。
func (s *Server) requireEditor(w http.ResponseWriter, channelID, userID string) bool {
	owner, err := s.store.GetChannelOwner(channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "channel_not_found", "頻道不存在")
			return false
		}
		writeErr(w, http.StatusInternalServerError, "owner_check_failed", err.Error())
		return false
	}
	if userID == owner {
		return true
	}

	role, err := s.store.GetMemberRole(channelID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusForbidden, "not_member", "你不是此頻道的成員")
			return false
		}
		writeErr(w, http.StatusInternalServerError, "role_check_failed", err.Error())
		return false
	}
	if role != model.RoleEditor {
		writeErr(w, http.StatusForbidden, "not_editor", "只有具編輯權限的成員能記事;你目前是查詢權限")
		return false
	}
	return true
}

// requireMember 檢查 userID 是否為頻道成員(owner 或任一角色皆可);
// 非成員時寫入錯誤回應並回 false。用於「查詢」等任何成員都能做、但須屬於頻道的操作。
// owner 恆視為成員(即使 members 表沒有對應列)。
func (s *Server) requireMember(w http.ResponseWriter, channelID, userID string) bool {
	owner, err := s.store.GetChannelOwner(channelID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "channel_not_found", "頻道不存在")
			return false
		}
		writeErr(w, http.StatusInternalServerError, "owner_check_failed", err.Error())
		return false
	}
	if userID == owner {
		return true
	}

	// 非 owner:須在 members 表中(任一角色)才放行。
	if _, err := s.store.GetMemberRole(channelID, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusForbidden, "not_member", "你不是此頻道的成員")
			return false
		}
		writeErr(w, http.StatusInternalServerError, "role_check_failed", err.Error())
		return false
	}
	return true
}

// POST /v1/channels/{id}/query  { "question": "..." }
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// 查詢會回傳頻道內的條目資料,須限頻道成員(owner 或任一角色),擋未登入訪客 / 非成員。
	if !s.requireMember(w, id, s.userFor(r).ID) {
		return
	}

	var body struct {
		Question string `json:"question"`
	}
	if !decode(w, r, &body) {
		return
	}
	q := strings.TrimSpace(body.Question)
	if q == "" {
		writeErr(w, http.StatusBadRequest, "empty_question", "question 不可為空")
		return
	}
	// 不再由 api 撈 pool:agent 依 assistant.md 自己呼叫 query_entries 查條目
	// (用 channelID 定位頻道),再以 present_entries 呈現相關條目。
	answer := s.analyzer.Answer(id, q)
	writeJSON(w, http.StatusOK, answer)
}

// POST /v1/channels/{id}/assist  { "text": "..." }
// owner 統一輸入:LLM 自主判斷「記錄事項」或「回答提問」。
// - 記錄(recorded):把輸入存成訊息,並由 record_entry 產生關聯的 Entry,回 { kind:"recorded", message }。
// - 回答(answer):不存訊息,回 { kind:"answer", answer }。
// 只有頻道 owner 能用;分析器須支援 Assist(want 引擎),否則回 501。
func (s *Server) handleAssist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := s.userFor(r)

	// 記事(統一輸入)屬「修改」操作,需 editor 角色(owner 預設即 editor)。
	if !s.requireEditor(w, id, user.ID) {
		return
	}

	assistant, ok := s.analyzer.(llm.Assistant)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "assist_unsupported", "目前分析器不支援統一輸入(需 -llm want)")
		return
	}

	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "empty_text", "text 不可為空")
		return
	}

	// 產生 messageID 供 agent 記錄 context 用。原話(message)不存後端:
	// 後端只收原話當 LLM 輸入,解析出的 entry 才落庫(emit 同步寫入)。
	// 原話由前端存進「裝置端 DB」(與 server 隔離,local-first)。
	msgID := "msg_" + newID()

	// linkMessage 傳 nil:不再於後端寫入 message / 建立 entry↔message 關聯。
	// 原話與其關聯改由各裝置端自行保存。
	res := assistant.AssistForSession(user.ID, id, msgID, text, nil)

	if res.Kind == "error" {
		writeErr(w, http.StatusInternalServerError, "assist_failed", res.Text)
		return
	}
	if res.Kind == "recorded" {
		// 記錄了 → entry 已由 emit 同步寫入後端。回傳本次寫入的 entry 給前端,
		// 前端據此更新顯示,並把對應原話存進自己的裝置端 DB。
		s.hub.Broadcast(id, map[string]any{"event": "entries_updated", "channelID": id})
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":     "recorded",
			"text":     text,
			"entryIDs": res.EntryIDs,
		})
		return
	}

	// 回答了 → 不存訊息,只回答案;若 agent 用 present_entries 輸出了條目,一併回給前端用列表顯示。
	entries := res.Entries
	if entries == nil {
		entries = []llm.AssistEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":    "answer",
		"answer":  res.Text,
		"entries": entries,
	})
}

// GET /v1/channels/{id}/entries — 頻道的日期/事件條目(LLM 從訊息解析,關聯訊息)。
func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
	s.writeEntries(w, r.PathValue("id"))
}

// DELETE /v1/channels/{id}/entries — 清空頻道的所有條目與行程(開發/測試重置用)。
// 屬破壞性操作,限頻道 owner。
func (s *Server) handleResetChannelData(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireOwner(w, id, s.userFor(r).ID) {
		return
	}
	s.resetChannel(w, id)
}

// GET /v1/channels/{id}/trips — 頻道的行程分組(後端依時間自動歸組)。
func (s *Server) handleListTrips(w http.ResponseWriter, r *http.Request) {
	s.writeTrips(w, r.PathValue("id"))
}

// GET /v1/channels/{id}/trips/{tripID}/entries — 某行程下的條目。
func (s *Server) handleListTripEntries(w http.ResponseWriter, r *http.Request) {
	s.writeTripEntries(w, r.PathValue("id"), r.PathValue("tripID"))
}

// ----- shared query helpers -----

func (s *Server) writeEntries(w http.ResponseWriter, channelID string) {
	entries, err := s.store.ListEntriesByChannel(channelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if entries == nil {
		entries = []model.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) writeTrips(w http.ResponseWriter, channelID string) {
	trips, err := s.store.ListTripsByChannel(channelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if trips == nil {
		trips = []model.Trip{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"trips": trips})
}

func (s *Server) writeTripEntries(w http.ResponseWriter, channelID, tripID string) {
	entries, err := s.store.ListEntriesByTrip(channelID, tripID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if entries == nil {
		entries = []model.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) resetChannel(w http.ResponseWriter, channelID string) {
	if err := s.store.DeleteChannelEntriesAndTrips(channelID); err != nil {
		writeErr(w, http.StatusInternalServerError, "reset_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset", "channel": channelID})
}

// ----- helpers -----

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "請求格式錯誤")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]string{"code": errCode, "message": msg},
	})
}
