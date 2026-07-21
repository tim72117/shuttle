package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tim72117/tripace/internal/llm"
	"github.com/tim72117/tripace/internal/store"
)

// POST /v1/channels/{id}/public-link — 建立（或取得已有）公開連結。
func (s *Server) handleCreatePublicLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := s.userFor(r)
	if !s.requireEditor(w, id, user.ID) {
		return
	}
	var body struct {
		Editable bool `json:"editable"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	info, err := s.store.GetPublicLink(id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		token, err := s.store.CreatePublicLink(id, user.ID, body.Editable)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"linkToken": token, "editable": body.Editable})
		return
	}
	// 已存在：若 editable 有變更則更新
	if info.Editable != body.Editable {
		if err := s.store.SetPublicLinkEditable(id, body.Editable); err != nil {
			writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		info.Editable = body.Editable
	}
	writeJSON(w, http.StatusOK, map[string]any{"linkToken": info.Token, "editable": info.Editable})
}

// GET /v1/channels/{id}/public-link — 查詢頻道的公開連結。
func (s *Server) handleGetPublicLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireMember(w, id, s.userFor(r).ID) {
		return
	}
	info, err := s.store.GetPublicLink(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "此頻道尚未建立公開連結")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"linkToken": info.Token, "editable": info.Editable})
}

// DELETE /v1/channels/{id}/public-link — 刪除公開連結（撤銷分享）。
func (s *Server) handleDeletePublicLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireEditor(w, id, s.userFor(r).ID) {
		return
	}
	if err := s.store.DeletePublicLink(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GET /v1/public/{token} — 無需登入，讀取公開分享的頻道資料。
func (s *Server) handlePublicView(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	info, err := s.store.GetPublicLinkChannel(token)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "找不到此分享連結")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}

	channelName, err := s.store.GetChannelName(info.ChannelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "channel_failed", err.Error())
		return
	}
	entries, err := s.store.ListEntriesByChannel(info.ChannelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "entries_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channelID":   info.ChannelID,
		"channelName": channelName,
		"editable":    info.Editable,
		"entries":     entries,
	})
}

// POST /v1/public/{token}/assist — 公開頁訪客送訊息（僅 editable 連結允許）。
func (s *Server) handlePublicAssist(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	info, err := s.store.GetPublicLinkChannel(token)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "找不到此分享連結")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if !info.Editable {
		writeErr(w, http.StatusForbidden, "read_only", "此連結為唯讀")
		return
	}

	var body struct {
		Text string `json:"text"`
		Lang string `json:"lang,omitempty"`
		// ClientToolsSessionID:同 handleAssist(api.go)——公開頁目前尚未接
		// 第二條 clienttools WS 連線,通常留空;trip_entry_* 工具在留空時
		// 會直接失敗,不影響其餘工具。保留欄位是為了與 AssistForSession
		// 的統一簽章對齊,萬一日後公開頁也要接上可直接沿用。
		ClientToolsSessionID string `json:"clientToolsSessionId,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		writeErr(w, http.StatusBadRequest, "invalid_body", "text 必填")
		return
	}

	assistant, ok := s.analyzer.(llm.Assistant)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "not_supported", "此伺服器不支援 assist")
		return
	}
	// Lang 為使用者設定的 LLM 回答語言偏好("zh-TW"/"en"),空字串由下游視為預設(繁體中文)。
	res := assistant.AssistForSession("public:"+token, info.ChannelID, "", body.Text, body.Lang, body.ClientToolsSessionID, nil)
	writeJSON(w, http.StatusOK, res)
}
