package api

import (
	"net/http"
	"time"

	"hush.app/server/internal/db"

	"github.com/go-chi/chi/v5"
)

// inviteInfoResponse is the response for GET /api/invites/:code (public, no auth).
type inviteInfoResponse struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
}

// InviteRoutes returns the router for GET /api/invites/:code (resolve invite code to server info).
func InviteRoutes(store db.Store) chi.Router {
	r := chi.NewRouter()
	h := &inviteHandler{store: store}
	r.Get("/{code}", h.getInviteByCode)
	return r
}

type inviteHandler struct {
	store db.Store
}

func (h *inviteHandler) getInviteByCode(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite code required"})
		return
	}
	inv, err := h.store.GetInviteByCode(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lookup invite"})
		return
	}
	if inv == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invite not found"})
		return
	}
	if time.Now().After(inv.ExpiresAt) || (inv.MaxUses > 0 && inv.Uses >= inv.MaxUses) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invite expired or no longer valid"})
		return
	}
	server, err := h.store.GetServerByID(r.Context(), inv.ServerID)
	if err != nil || server == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}
	writeJSON(w, http.StatusOK, inviteInfoResponse{ServerID: inv.ServerID, ServerName: server.Name})
}
