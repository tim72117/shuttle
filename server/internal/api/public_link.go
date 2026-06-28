package api

import (
	"errors"
	"net/http"

	"github.com/channel/server/internal/store"
)

// POST /v1/channels/{id}/public-link — 建立（或取得已有）公開連結。
// editor 以上才能操作。
func (s *Server) handleCreatePublicLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := s.userFor(r)
	if !s.requireEditor(w, id, user.ID) {
		return
	}

	// 已有就直接回傳，避免重複建立。
	token, err := s.store.GetPublicLink(id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		token, err = s.store.CreatePublicLink(id, user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"linkToken": token})
}

// GET /v1/channels/{id}/public-link — 查詢頻道的公開連結。
func (s *Server) handleGetPublicLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireMember(w, id, s.userFor(r).ID) {
		return
	}
	token, err := s.store.GetPublicLink(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "此頻道尚未建立公開連結")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"linkToken": token})
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

// GET /public/{token} — 無需登入，讀取公開分享的頻道資料（唯讀）。
func (s *Server) handlePublicView(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	channelID, err := s.store.GetPublicLinkChannel(token)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "找不到此分享連結")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}

	channelName, err := s.store.GetChannelName(channelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "channel_failed", err.Error())
		return
	}
	entries, err := s.store.ListEntriesByChannel(channelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "entries_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channelID":   channelID,
		"channelName": channelName,
		"entries":     entries,
	})
}
