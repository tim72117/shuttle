// Package api 提供對齊 docs/API.md 的 HTTP handlers。
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tim72117/tripace/internal/auth"
	"github.com/tim72117/tripace/internal/llm"
	"github.com/tim72117/tripace/internal/model"
	"github.com/tim72117/tripace/internal/store"
	"github.com/tim72117/tripace/internal/toolschema"
	"github.com/tim72117/tripace/internal/tripsvc"
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

	// clientTools* 是「LLM 呼叫前端 tool」試做(POC)專用的狀態,與上面 store/
	// analyzer/hub 等正式對話流程完全分離——見 clienttools_http.go/
	// clienttools_ws.go。nil(未呼叫 EnableClientTools)時,對應的
	// /internal/clienttools/* 端點回 503,不影響其餘路由。
	clientToolsRegistry *toolschema.Registry
	clientToolsAnalyzer *llm.ClientToolsAnalyzer
	clientToolsSessions *clientToolsSessions
}

func New(st *store.Store, an llm.Analyzer, signer *auth.Signer, devMode bool) *Server {
	return &Server{
		store:     st,
		analyzer:  an,
		signer:    signer,
		hub:       newHub(),
		devMode:   devMode,
		guestUser: model.User{ID: "usr_me", Name: "我", AvatarColor: "#8C7B6A"},
	}
}

// EnableClientTools wires the "LLM calls a frontend tool" POC's
// /internal/clienttools/* endpoints (see clienttools_http.go/
// clienttools_ws.go). Optional and separate from New() (rather than a
// constructor parameter) so main.go can call it only when a real want
// analyzer is available — this POC has no meaning under -llm mock, and
// New() itself has no such precondition for its other args.
func (s *Server) EnableClientTools(registry *toolschema.Registry, analyzer *llm.ClientToolsAnalyzer) {
	s.clientToolsRegistry = registry
	s.clientToolsAnalyzer = analyzer
	s.clientToolsSessions = newClientToolsSessions()
}

// NotifyEntriesUpdated 廣播 entries_updated 給指定頻道的訂閱者(供 wanttools 呼叫)。
func (s *Server) NotifyEntriesUpdated(channelID string) {
	s.hub.Broadcast(channelID, map[string]any{"event": "entries_updated", "channelID": channelID})
}

// NotifyEntryUpdating 廣播 entry_updating(帶 entryID)給指定頻道的訂閱者(供 wanttools 呼叫),
// 讓前端在工具更新該條目期間顯示「更新中」動畫。
func (s *Server) NotifyEntryUpdating(channelID, entryID string) {
	s.hub.Broadcast(channelID, map[string]any{"event": "entry_updating", "channelID": channelID, "entryID": entryID})
}

// NotifyAskUser 廣播 ask_user(帶 askType/prompt)給指定頻道的訂閱者(供 wanttools 呼叫),
// 讓前端開啟對應 UI(如日期選擇器)請使用者補上缺失資訊。
func (s *Server) NotifyAskUser(channelID, askType, prompt string) {
	s.hub.Broadcast(channelID, map[string]any{"event": "ask_user", "channelID": channelID, "askType": askType, "prompt": prompt})
}

// NotifyAskChoice 廣播 ask_choice(帶 prompt/options)給指定頻道的訂閱者(供 wanttools 呼叫),
// 讓前端開啟選單 UI 請使用者從 options 中選一個。
func (s *Server) NotifyAskChoice(channelID, prompt string, options []map[string]any) {
	s.hub.Broadcast(channelID, map[string]any{"event": "ask_choice", "channelID": channelID, "prompt": prompt, "options": options})
}

// NotifyTaskCreated 廣播 task_created(帶 taskID/date/text/kind)給指定頻道的訂閱者(供 wanttools 呼叫),
// 讓前端在該日期下插入一張標示動作(新增/更新)的佔位卡。
func (s *Server) NotifyTaskCreated(channelID string, taskID int, date, text, kind string) {
	s.hub.Broadcast(channelID, map[string]any{
		"event": "task_created", "channelID": channelID, "taskID": taskID, "date": date, "text": text, "kind": kind,
	})
}

// NotifyTaskEntryReady 廣播 task_entry_ready(帶 taskID/entryID)給指定頻道的訂閱者(供 wanttools 呼叫),
// 讓前端把對應的佔位卡直接替換成正式條目卡。
func (s *Server) NotifyTaskEntryReady(channelID string, taskID int, entryID string) {
	s.hub.Broadcast(channelID, map[string]any{
		"event": "task_entry_ready", "channelID": channelID, "taskID": taskID, "entryID": entryID,
	})
}

// NotifyEntriesLoaded 廣播 entries_loaded(帶 entry_query 查到、轉換成前端
// TripEntry 格式的條目清單)給指定頻道的訂閱者(供 wanttools 的 entry_query
// 工具呼叫),讓前端合併進旅程清單表格供使用者查看/編輯。entries 用
// []map[string]any 而非具名型別,避免 api 套件為了此簽章反向依賴 wanttools
// (見 wanttools.TripEntryPayload 的定義處)。
func (s *Server) NotifyEntriesLoaded(channelID string, entries []map[string]any) {
	s.hub.Broadcast(channelID, map[string]any{
		"event": "entries_loaded", "channelID": channelID, "entries": entries,
	})
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
	mux.HandleFunc("POST /v1/channels/{id}/entries", s.handleCreateTripEntry)
	mux.HandleFunc("DELETE /v1/channels/{id}/entries", s.handleResetChannelData)
	mux.HandleFunc("PUT /v1/channels/{id}/entries/{entryID}", s.handleUpdateTripEntry)
	mux.HandleFunc("DELETE /v1/channels/{id}/entries/{entryID}", s.handleDeleteTripEntry)
	mux.HandleFunc("PATCH /v1/entries/{id}", s.handleUpdateEntry)
	mux.HandleFunc("GET /v1/channels/{id}/trips", s.handleListTrips)
	mux.HandleFunc("GET /v1/channels/{id}/trips/{tripID}/entries", s.handleListTripEntries)
	mux.HandleFunc("GET /v1/channels/{id}/ws", s.handleWS)
	mux.HandleFunc("POST /v1/channels/{id}/public-link", s.handleCreatePublicLink)
	mux.HandleFunc("GET /v1/channels/{id}/public-link", s.handleGetPublicLink)
	mux.HandleFunc("DELETE /v1/channels/{id}/public-link", s.handleDeletePublicLink)
	mux.HandleFunc("GET /v1/public/{token}", s.handlePublicView)
	mux.HandleFunc("POST /v1/public/{token}/assist", s.handlePublicAssist)

	// internal — 供 CLI(cmd/cli)/自動化腳本操作資料,不走使用者登入驗證,
	// 改用 internalAuth 檢查共享密鑰(INTERNAL_API_TOKEN),避免任何知道
	// entryID/channelID 的外部呼叫者繞過上面 /v1/* 的 requireOwner/
	// requireEditor 檢查(這兩組路由掛在同一個對外 port,路徑命名本身
	// 不構成安全邊界,見 middleware.go internalAuth 的說明)。
	internalMux := http.NewServeMux()
	internalMux.HandleFunc("GET /internal/channels", s.handleInternalListChannels)
	internalMux.HandleFunc("POST /internal/channels/{id}/notify", s.handleNotify)
	internalMux.HandleFunc("POST /internal/channels/{id}/entries", s.handleInternalRecord)
	internalMux.HandleFunc("POST /internal/entries/{id}/trip", s.handleInternalAddToTrip)
	internalMux.HandleFunc("PATCH /internal/entries/{id}", s.handleInternalUpdateEntry)
	internalMux.HandleFunc("PATCH /internal/entries/{id}/latlng", s.handleInternalSetLatLng)
	internalMux.HandleFunc("GET /internal/channels/{id}/trips", s.handleInternalListTrips)
	internalMux.HandleFunc("GET /internal/channels/{id}/trips/{tripID}/entries", s.handleInternalTripEntries)
	internalMux.HandleFunc("DELETE /internal/channels/{id}/entries", s.handleInternalReset)

	// clienttools — 「LLM 呼叫前端 tool」試做(POC)專用端點,見
	// clienttools_http.go/clienttools_ws.go。與上面既有 /internal/* 端點
	// 一樣掛在 internalAuth 之後(INTERNAL_API_TOKEN 未設時完全不受保護,
	// 符合本試做「不需要驗證,僅本機試做用」的要求)。EnableClientTools
	// 未呼叫時(main.go 未啟用或 want 分析器初始化失敗)這幾個 handler
	// 內部會各自回 503,不需要在路由層額外判斷。
	internalMux.HandleFunc("GET /internal/clienttools/ws", s.handleClientToolsWS)
	internalMux.HandleFunc("POST /internal/clienttools/test-prompt", s.handleClientToolsTestPrompt)
	internalMux.HandleFunc("GET /internal/clienttools/info", s.handleClientToolsInfo)

	mux.Handle("/internal/", internalAuth(internalMux))

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
		Lang     string `json:"lang,omitempty"`
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
	// Lang 為使用者設定的 LLM 回答語言偏好("zh-TW"/"en"),空字串由下游視為預設(繁體中文)。
	answer := s.analyzer.Answer(id, q, body.Lang)
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
		Lang string `json:"lang,omitempty"`
		// ClientToolsSessionID:前端 ChatScreen.tsx 另開的第二條 WS 連線
		// (/internal/clienttools/ws)收到 ack 後拿到的 sessionId(見
		// clienttools_ws.go handleHello)。帶上這個欄位,want_analyzer.go 的
		// Assist 才能透過 orch.SetSessionEnvs 把它交給 trip_entry_* 工具,
		// 讓工具執行時經 ctx.GetSessionEnvs() 找到同一個 WS session、把呼叫
		// 轉發回瀏覽器分頁(見 clienttools/interaction.go 的 askPage)。
		// 空字串(前端尚未連上第二條 WS,或這次改動前的舊前端)時,
		// trip_entry_* 工具呼叫會直接失敗回「no session id on this call」
		// ——不影響其餘工具(entry_query/entry_delete/geocode 等)照常運作。
		ClientToolsSessionID string `json:"clientToolsSessionId,omitempty"`
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
	// Lang 為使用者設定的 LLM 回答語言偏好("zh-TW"/"en"),空字串由下游視為預設(繁體中文)。
	res := assistant.AssistForSession(user.ID, id, msgID, text, body.Lang, body.ClientToolsSessionID, nil)

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

	// 回答了 → 不存訊息,只回答案;若 agent 用 present_entries 輸出了條目、
	// 或用 recommend_nearby 查到了候選景點,一併回給前端掛在該則訊息下方顯示。
	entries := res.Entries
	if entries == nil {
		entries = []llm.AssistEntry{}
	}
	places := res.RecommendedPlaces
	if places == nil {
		places = []llm.AssistPlace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":              "answer",
		"answer":            res.Text,
		"entries":           entries,
		"recommendedPlaces": places,
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

// tripEntryBody 是前端旅程清單「儲存」動作的請求/回應共用形狀,欄位對齊前端
// TripEntry(web/src/clienttools/tripEntryTools.ts 的 {id, title, date, time,
// note})與 server/tools/clienttools.yaml 的 trip_entry_add/trip_entry_update
// 命名——這三個端點是給前端「儲存」按鈕呼叫的一般 REST API(逐筆
// upsert),不經過 wanttools/want 那套 LLM 工具註冊機制,故欄位命名直接對齊
// 前端型別即可,不需要跟 entry_query.go 的 TripEntryPayload 共用型別
// (兩邊各自獨立、只是欄位剛好同名同義)。
type tripEntryBody struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	Date  string `json:"date"`
	Time  string `json:"time"`
	Note  string `json:"note"`
}

// POST /v1/channels/{id}/entries — 前端旅程清單「儲存」新增一筆(不含 id,由後端產生)。
// body 帶 TripEntry 格式(title/date/time/note);成功回傳含新產生 id 的完整 tripEntryBody。
// 屬「修改」操作,需 editor 角色(owner 預設即 editor,同 handleAssist 的權限慣例)。
func (s *Server) handleCreateTripEntry(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	if !s.requireEditor(w, channelID, s.userFor(r).ID) {
		return
	}

	var body tripEntryBody
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeErr(w, http.StatusBadRequest, "invalid_title", "title 不可為空")
		return
	}

	svc := tripsvc.New(s.store, nil)
	res, err := svc.Record(tripsvc.RecordInput{
		ChannelID: channelID,
		Title:     body.Title,
		Start:     body.Date,
		StartTime: body.Time,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	// note 目前無獨立寫入欄位(RecordInput 沒有 Note),新增後若帶了 note 用
	// UpdateEntry 補上——tripsvc.Record 專注在「新增 + 找候選行程」,note 是
	// 手動編輯情境的欄位,沿用 UpdateEntry 的既有寫入路徑,不擴大 RecordInput
	// 的職責。
	if body.Note != "" {
		if err := svc.UpdateEntry(tripsvc.UpdateEntryInput{ID: res.EntryID, Note: body.Note}); err != nil {
			writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
	}
	s.hub.Broadcast(channelID, map[string]any{"event": "entries_updated", "channelID": channelID})
	writeJSON(w, http.StatusCreated, tripEntryBody{ID: res.EntryID, Title: body.Title, Date: body.Date, Time: body.Time, Note: body.Note})
}

// PUT /v1/channels/{id}/entries/{entryID} — 前端旅程清單「儲存」修改既有一筆。
// body 帶要更新的欄位(TripEntry 格式);entryID 須屬於路徑上的 channelID,
// 避免前端誤帶其他頻道的 id 時跨頻道修改到別人的資料。
func (s *Server) handleUpdateTripEntry(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	entryID := r.PathValue("entryID")
	if !s.requireEditor(w, channelID, s.userFor(r).ID) {
		return
	}

	entry, err := s.store.GetEntry(entryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "entry_not_found", "條目不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if entry.ChannelID != channelID {
		writeErr(w, http.StatusNotFound, "entry_not_found", "條目不存在")
		return
	}

	var body tripEntryBody
	if !decode(w, r, &body) {
		return
	}

	svc := tripsvc.New(s.store, nil)
	if err := svc.UpdateEntry(tripsvc.UpdateEntryInput{
		ID:        entryID,
		Title:     body.Title,
		Start:     body.Date,
		StartTime: body.Time,
		Note:      body.Note,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	s.hub.Broadcast(channelID, map[string]any{"event": "entries_updated", "channelID": channelID})
	writeJSON(w, http.StatusOK, map[string]string{"updated": entryID})
}

// DELETE /v1/channels/{id}/entries/{entryID} — 前端旅程清單「儲存」刪除既有一筆
// (使用者在前端表格移除該列後,儲存時觸發)。entryID 須屬於路徑上的
// channelID,理由同 handleUpdateTripEntry。
func (s *Server) handleDeleteTripEntry(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	entryID := r.PathValue("entryID")
	if !s.requireEditor(w, channelID, s.userFor(r).ID) {
		return
	}

	entry, err := s.store.GetEntry(entryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "entry_not_found", "條目不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if entry.ChannelID != channelID {
		writeErr(w, http.StatusNotFound, "entry_not_found", "條目不存在")
		return
	}

	if err := s.store.DeleteEntry(entryID); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	s.hub.Broadcast(channelID, map[string]any{"event": "entries_updated", "channelID": channelID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": entryID})
}

// PATCH /v1/entries/{id} — 手動編輯條目(不經 AI,前端表單直接送出要改的欄位)。
// entryID 本身不帶 channelID,故先查出該條目所屬頻道,再依 editor 權限放行;
// 只更新請求帶了值的欄位(空字串視為不改,見 store.UpdateEntry),未帶到的欄位維持原值。
func (s *Server) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	entryID := r.PathValue("id")
	entry, err := s.store.GetEntry(entryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "entry_not_found", "條目不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if !s.requireEditor(w, entry.ChannelID, s.userFor(r).ID) {
		return
	}

	var body struct {
		Title     string         `json:"title"`
		Start     string         `json:"start"`
		StartTime string         `json:"startTime"`
		End       string         `json:"end"`
		EndTime   string         `json:"endTime"`
		Location  string         `json:"location"`
		Note      string         `json:"note"`
		Kind      string         `json:"kind"`
		Detail    map[string]any `json:"detail"`
	}
	if !decode(w, r, &body) {
		return
	}

	svc := tripsvc.New(s.store, nil)
	if err := svc.UpdateEntry(tripsvc.UpdateEntryInput{
		ID:        entryID,
		Title:     body.Title,
		Start:     body.Start,
		StartTime: body.StartTime,
		End:       body.End,
		EndTime:   body.EndTime,
		Location:  body.Location,
		Note:      body.Note,
		Kind:      body.Kind,
		Detail:    body.Detail,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	s.hub.Broadcast(entry.ChannelID, map[string]any{"event": "entries_updated", "channelID": entry.ChannelID})
	writeJSON(w, http.StatusOK, map[string]string{"updated": entryID})
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
