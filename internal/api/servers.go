package api

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
)

const (
	roleAdmin      = "admin"
	roleMember     = "member"
	maxNameLength  = 100
)

// serverWithChannelsResponse is the response for GET /api/servers/:id.
type serverWithChannelsResponse struct {
	Server   models.Server    `json:"server"`
	Channels []models.Channel `json:"channels"`
	MyRole   string           `json:"myRole"`
}

// ServerRoutes returns the router for /api/servers (create, list, get, update, delete, join, leave, create/list channels).
func ServerRoutes(store db.Store, jwtSecret string) chi.Router {
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	h := &serverHandler{store: store}
	r.Post("/", h.createServer)
	r.Get("/", h.listServers)
	r.Get("/{id}/members", h.listMembers)
	r.Get("/{id}", h.getServer)
	r.Put("/{id}", h.updateServer)
	r.Delete("/{id}", h.deleteServer)
	r.Post("/{id}/join", h.joinServer)
	r.Post("/{id}/leave", h.leaveServer)
	r.Post("/{id}/channels", h.createChannel)
	r.Get("/{id}/channels", h.listChannels)
	r.Post("/{id}/invites", h.createInvite)
	return r
}

type serverHandler struct {
	store db.Store
}

func (h *serverHandler) createServer(w http.ResponseWriter, r *http.Request) {
	var req models.CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if len(req.Name) > maxNameLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name exceeds maximum length"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	server, err := h.store.CreateServerWithOwner(r.Context(), req.Name, req.IconURL, userID)
	if err != nil {
		slog.Error("create server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create server"})
		return
	}
	writeJSON(w, http.StatusCreated, server)
}

func (h *serverHandler) listServers(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	list, err := h.store.ListServersForUser(r.Context(), userID)
	if err != nil {
		slog.Error("list servers", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list servers"})
		return
	}
	if list == nil {
		list = []models.ServerWithRole{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *serverHandler) getServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil {
		slog.Error("get server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check membership"})
		return
	}
	if member == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a member of this server"})
		return
	}
	server, err := h.store.GetServerByID(r.Context(), serverID)
	if err != nil || server == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}
	channels, err := h.store.ListChannels(r.Context(), serverID)
	if err != nil {
		slog.Error("list channels", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load channels"})
		return
	}
	if channels == nil {
		channels = []models.Channel{}
	}
	writeJSON(w, http.StatusOK, serverWithChannelsResponse{
		Server: *server, Channels: channels, MyRole: member.Role,
	})
}

func (h *serverHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil {
		slog.Error("get server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check membership"})
		return
	}
	if member == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a member of this server"})
		return
	}
	list, err := h.store.ListServerMembers(r.Context(), serverID)
	if err != nil {
		slog.Error("list server members", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load members"})
		return
	}
	if list == nil {
		list = []models.ServerMemberWithUser{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"members": list})
}

func (h *serverHandler) updateServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil || member == nil || member.Role != roleAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
	var req models.UpdateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if len(trimmed) > maxNameLength {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name exceeds maximum length"})
			return
		}
		req.Name = &trimmed
	}
	if err := h.store.UpdateServer(r.Context(), serverID, req.Name, req.IconURL); err != nil {
		slog.Error("update server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update server"})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *serverHandler) deleteServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil || member == nil || member.Role != roleAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
	if err := h.store.DeleteServer(r.Context(), serverID); err != nil {
		slog.Error("delete server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete server"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *serverHandler) joinServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.JoinServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	code := strings.TrimSpace(req.InviteCode)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite code is required"})
		return
	}
	inv, err := h.store.GetInviteByCode(r.Context(), code)
	if err != nil {
		slog.Error("get invite", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate invite"})
		return
	}
	if inv == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired invite code"})
		return
	}
	if inv.ServerID != serverID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite code is for a different server"})
		return
	}
	existing, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil {
		slog.Error("get server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check membership"})
		return
	}
	if existing != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already a member of this server"})
		return
	}
	claimed, err := h.store.ClaimInviteUse(r.Context(), code)
	if err != nil {
		slog.Error("claim invite use", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to join server"})
		return
	}
	if !claimed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite code has expired or reached maximum uses"})
		return
	}
	if err := h.store.AddServerMember(r.Context(), serverID, userID, roleMember); err != nil {
		slog.Error("add server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to join server"})
		return
	}
	server, err := h.store.GetServerByID(r.Context(), serverID)
	if err != nil || server == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load server"})
		return
	}
	writeJSON(w, http.StatusOK, server)
}

func (h *serverHandler) leaveServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil || member == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a member of this server"})
		return
	}
	count, err := h.store.CountServerMembers(r.Context(), serverID)
	if err != nil {
		slog.Error("count server members", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave server"})
		return
	}
	if count <= 1 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot leave: sole member; delete the server instead"})
		return
	}
	server, err := h.store.GetServerByID(r.Context(), serverID)
	if err != nil {
		slog.Error("get server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave server"})
		return
	}
	isOwner := server != nil && server.OwnerID == userID
	if isOwner {
		candidate, err := h.store.GetNextOwnerCandidate(r.Context(), serverID, userID)
		if err != nil {
			slog.Error("get next owner candidate", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave server"})
			return
		}
		if candidate != nil {
			if err := h.store.TransferServerOwnership(r.Context(), serverID, candidate.UserID); err != nil {
				slog.Error("transfer ownership", "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave server"})
				return
			}
			if err := h.store.UpdateServerMemberRole(r.Context(), serverID, candidate.UserID, roleAdmin); err != nil {
				slog.Error("update new owner role", "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave server"})
				return
			}
		}
	}
	if err := h.store.RemoveServerMember(r.Context(), serverID, userID); err != nil {
		slog.Error("remove server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave server"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *serverHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil || member == nil || member.Role != roleAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
	var req models.CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if len(req.Name) > maxNameLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name exceeds maximum length"})
		return
	}
	if req.Type != "text" && req.Type != "voice" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type must be text or voice"})
		return
	}
	position := 0
	if req.Position != nil {
		position = *req.Position
	}
	var voiceMode *string
	if req.Type == "voice" {
		if req.VoiceMode == nil || (*req.VoiceMode != "low-latency" && *req.VoiceMode != "quality") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "voice_mode is required for voice channels and must be low-latency or quality"})
			return
		}
		voiceMode = req.VoiceMode
	} else {
		if req.VoiceMode != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "voice_mode is only allowed for voice channels"})
			return
		}
	}
	ch, err := h.store.CreateChannel(r.Context(), serverID, req.Name, req.Type, voiceMode, req.ParentID, position)
	if err != nil {
		slog.Error("create channel", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create channel"})
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (h *serverHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil || member == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a member of this server"})
		return
	}
	channels, err := h.store.ListChannels(r.Context(), serverID)
	if err != nil {
		slog.Error("list channels", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list channels"})
		return
	}
	if channels == nil {
		channels = []models.Channel{}
	}
	writeJSON(w, http.StatusOK, channels)
}

const (
	defaultInviteMaxUses   = 50
	defaultInviteExpiresIn = 7 * 24 * 3600 // 7 days in seconds
	inviteCodeLength       = 8
	inviteCodeAlphabet     = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
)

func generateInviteCode() (string, error) {
	b := make([]byte, inviteCodeLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(inviteCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = inviteCodeAlphabet[n.Int64()]
	}
	return string(b), nil
}

func (h *serverHandler) createInvite(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "id")
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	member, err := h.store.GetServerMember(r.Context(), serverID, userID)
	if err != nil || member == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a member of this server"})
		return
	}

	var req models.CreateInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is fine — use defaults
		req = models.CreateInviteRequest{}
	}

	maxUses := defaultInviteMaxUses
	if req.MaxUses != nil {
		if *req.MaxUses < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxUses must be at least 1"})
			return
		}
		maxUses = *req.MaxUses
	}

	expiresInSec := defaultInviteExpiresIn
	if req.ExpiresIn != nil {
		if *req.ExpiresIn < 60 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expiresIn must be at least 60 seconds"})
			return
		}
		expiresInSec = *req.ExpiresIn
	}
	expiresAt := time.Now().Add(time.Duration(expiresInSec) * time.Second)

	code, err := generateInviteCode()
	if err != nil {
		slog.Error("generate invite code", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate invite"})
		return
	}

	inv, err := h.store.CreateInvite(r.Context(), code, serverID, userID, maxUses, expiresAt)
	if err != nil {
		slog.Error("create invite", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create invite"})
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}
