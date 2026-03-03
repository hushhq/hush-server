package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
)

// GlobalBroadcaster is satisfied by *ws.Hub. Used for dependency injection in tests.
type GlobalBroadcaster interface {
	BroadcastToAll(message []byte)
}

// roleOrder maps role names to their numeric rank for comparison.
var roleOrder = map[string]int{
	"owner":  3,
	"admin":  2,
	"mod":    1,
	"member": 0,
}

// roleAtLeast returns true if userRole is at least as privileged as required.
func roleAtLeast(userRole, required string) bool {
	return roleOrder[userRole] >= roleOrder[required]
}

// InstanceRoutes returns the router for /api/instance.
func InstanceRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string) chi.Router {
	h := &instanceHandler{store: store, hub: hub}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/", h.getConfig)
	r.Put("/", h.updateConfig)
	r.Get("/members", h.listMembers)
	return r
}

type instanceHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// instanceConfigResponse extends InstanceConfig with a bootstrapped flag and the caller's role.
type instanceConfigResponse struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	IconURL          *string `json:"iconUrl"`
	OwnerID          *string `json:"ownerId"`
	RegistrationMode string  `json:"registrationMode"`
	Bootstrapped     bool    `json:"bootstrapped"`
	MyRole           string  `json:"myRole"`
}

func (h *instanceHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("get instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load instance config"})
		return
	}
	userID := userIDFromContext(r.Context())
	role, err := h.store.GetUserRole(r.Context(), userID)
	if err != nil {
		slog.Error("get user role for instance config", "err", err)
		role = "member"
	}
	writeJSON(w, http.StatusOK, instanceConfigResponse{
		ID:               cfg.ID,
		Name:             cfg.Name,
		IconURL:          cfg.IconURL,
		OwnerID:          cfg.OwnerID,
		RegistrationMode: cfg.RegistrationMode,
		Bootstrapped:     cfg.OwnerID != nil,
		MyRole:           role,
	})
}

// updateConfigRequest is the JSON body for PUT /api/instance.
type updateConfigRequest struct {
	Name             *string `json:"name"`
	IconURL          *string `json:"iconUrl"`
	RegistrationMode *string `json:"registrationMode"`
}

func (h *instanceHandler) updateConfig(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	role, err := h.store.GetUserRole(r.Context(), userID)
	if err != nil {
		slog.Error("get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(role, "owner") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "owner role required"})
		return
	}
	var req updateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name cannot be empty"})
			return
		}
		req.Name = &trimmed
	}
	if req.RegistrationMode != nil {
		switch *req.RegistrationMode {
		case "open", "invite_only", "closed":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "registrationMode must be open, invite_only, or closed"})
			return
		}
	}
	if err := h.store.UpdateInstanceConfig(r.Context(), req.Name, req.IconURL, req.RegistrationMode); err != nil {
		slog.Error("update instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update instance config"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type": "instance_updated",
		})
		h.hub.BroadcastToAll(msg)
	}
}

func (h *instanceHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := h.store.ListMembers(r.Context())
	if err != nil {
		slog.Error("list members", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list members"})
		return
	}
	if members == nil {
		members = []models.Member{}
	}
	writeJSON(w, http.StatusOK, members)
}
