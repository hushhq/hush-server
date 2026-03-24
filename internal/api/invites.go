package api

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
)

const (
	defaultInviteMaxUses   = 50
	defaultInviteExpiresIn = 7 * 24 * 3600 // 7 days in seconds
	inviteCodeLength       = 8
	inviteCodeAlphabet     = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
)

// GuildInviteRoutes returns the router for guild-scoped invite creation.
// Mounted under /api/servers/{serverId}/invites.
// Auth and RequireGuildMember are applied by the parent router.
func GuildInviteRoutes(store db.Store) chi.Router {
	h := &inviteHandler{store: store}
	r := chi.NewRouter()
	r.Post("/", h.createInvite)
	return r
}

// PublicInviteRoutes returns the router for invite resolution and claiming.
// Mounted at /api/invites. Auth is applied inside for the claim route.
func PublicInviteRoutes(store db.Store, jwtSecret string, hub GlobalBroadcaster) chi.Router {
	h := &inviteHandler{store: store, hub: hub}
	r := chi.NewRouter()
	// Public: resolve invite info before login (unauthenticated).
	r.Get("/{code}", h.getInviteInfo)
	// Authenticated: claim an invite. User is NOT a guild member yet — no RequireGuildMember.
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(jwtSecret, store))
		r.Post("/claim", h.claimInvite)
	})
	return r
}

type inviteHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// inviteInfoResponse is returned for public GET /api/invites/:code.
// Includes serverId so the frontend can navigate to the guild after claim.
type inviteInfoResponse struct {
	Code      string `json:"code"`
	GuildName string `json:"guildName"`
	ExpiresAt string `json:"expiresAt"`
	ServerID  string `json:"serverId"`
}

func (h *inviteHandler) getInviteInfo(w http.ResponseWriter, r *http.Request) {
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
	if inv.ServerID == nil || *inv.ServerID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invite not associated with a guild"})
		return
	}
	guild, err := h.store.GetServerByID(r.Context(), *inv.ServerID)
	if err != nil || guild == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load guild info"})
		return
	}
	// TODO(0O-03): GuildName is empty — guild name is now encrypted; client decrypts after joining.
	writeJSON(w, http.StatusOK, inviteInfoResponse{
		Code:      inv.Code,
		GuildName: "",
		ExpiresAt: inv.ExpiresAt.Format(time.RFC3339),
		ServerID:  guild.ID,
	})
}

// createInviteRequest is the JSON body for POST /api/servers/{serverId}/invites.
type createInviteRequest struct {
	MaxUses   *int `json:"maxUses"`
	ExpiresIn *int `json:"expiresIn"` // seconds
}

func (h *inviteHandler) createInvite(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	role := guildRoleFromContext(r.Context())
	if !roleAtLeast(role, "mod") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod role or higher required to create invites"})
		return
	}
	var req createInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req = createInviteRequest{}
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
	inv, err := h.store.CreateInvite(r.Context(), serverID, code, userID, maxUses, expiresAt)
	if err != nil {
		slog.Error("create invite", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create invite"})
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}

// claimInviteRequest is the JSON body for POST /api/invites/claim.
type claimInviteRequest struct {
	Code string `json:"code"`
}

// claimInviteResponse is returned after a successful invite claim.
type claimInviteResponse struct {
	ServerID  string `json:"serverId"`
	GuildName string `json:"guildName"`
}

func (h *inviteHandler) claimInvite(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req claimInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
		return
	}
	inv, err := h.store.GetInviteByCode(r.Context(), req.Code)
	if err != nil {
		slog.Error("claimInvite: get invite by code", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate invite"})
		return
	}
	if inv == nil || time.Now().After(inv.ExpiresAt) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired invite code"})
		return
	}
	if inv.ServerID == nil || *inv.ServerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite not associated with a guild"})
		return
	}
	serverID := *inv.ServerID

	// Guild-scoped ban check — does not prevent the user from using other guilds (IROLE-04).
	ban, err := h.store.GetActiveBan(r.Context(), serverID, userID)
	if err != nil {
		slog.Error("claimInvite: check ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check ban status"})
		return
	}
	if ban != nil {
		// TODO(0O-03): guild name is encrypted; use server ID as fallback.
		resp := map[string]interface{}{
			"error": "You are banned from this guild.",
		}
		if ban.ExpiresAt != nil {
			resp["ban_expires_at"] = ban.ExpiresAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusForbidden, resp)
		return
	}

	claimed, err := h.store.ClaimInviteUse(r.Context(), req.Code)
	if err != nil {
		slog.Error("claimInvite: claim invite use", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to claim invite"})
		return
	}
	if !claimed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite code has reached maximum uses"})
		return
	}

	// Add the user to the guild as a member (permission level 0).
	if err := h.store.AddServerMember(r.Context(), serverID, userID, models.PermissionLevelMember); err != nil {
		slog.Error("claimInvite: add server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to join guild"})
		return
	}

	// TODO(0O-03): guild name is encrypted, cannot include in member_joined payload.
	// Broadcast member_joined so other connected users see the new member.
	if h.hub != nil {
		member := map[string]interface{}{
			"id":   userID,
			"role": "member",
		}
		if u, err := h.store.GetUserByID(r.Context(), userID); err == nil && u != nil {
			member["username"] = u.Username
			member["displayName"] = u.DisplayName
		}
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_joined",
			"server_id": serverID,
			"member":    member,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}

	// Emit system message: the joining user is the actor, no target.
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_joined", userID, nil, "", nil)

	// TODO(0O-03): GuildName is empty — guild name is encrypted; client decrypts via MLS group.
	writeJSON(w, http.StatusOK, claimInviteResponse{
		ServerID:  serverID,
		GuildName: "",
	})
}

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
