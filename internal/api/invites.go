package api

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"hush.app/server/internal/db"

	"github.com/go-chi/chi/v5"
)

const (
	defaultInviteMaxUses   = 50
	defaultInviteExpiresIn = 7 * 24 * 3600 // 7 days in seconds
	inviteCodeLength       = 8
	inviteCodeAlphabet     = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
)

// InviteRoutes returns the router for /api/invites (instance-global).
func InviteRoutes(store db.Store, jwtSecret string) chi.Router {
	h := &inviteHandler{store: store}
	r := chi.NewRouter()
	// Public: resolve invite info before login
	r.Get("/{code}", h.getInviteInfo)
	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(jwtSecret, store))
		r.Post("/", h.createInvite)
		r.Post("/claim", h.claimInvite)
	})
	return r
}

type inviteHandler struct {
	store db.Store
}

// inviteInfoResponse is returned for public GET /api/invites/:code.
type inviteInfoResponse struct {
	Code         string `json:"code"`
	InstanceName string `json:"instanceName"`
	ExpiresAt    string `json:"expiresAt"`
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
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil || cfg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load instance config"})
		return
	}
	writeJSON(w, http.StatusOK, inviteInfoResponse{
		Code:         inv.Code,
		InstanceName: cfg.Name,
		ExpiresAt:    inv.ExpiresAt.Format(time.RFC3339),
	})
}

// createInviteRequest is the JSON body for POST /api/invites.
type createInviteRequest struct {
	MaxUses   *int `json:"maxUses"`
	ExpiresIn *int `json:"expiresIn"` // seconds
}

func (h *inviteHandler) createInvite(w http.ResponseWriter, r *http.Request) {
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
	inv, err := h.store.CreateInvite(r.Context(), code, userID, maxUses, expiresAt)
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

func (h *inviteHandler) claimInvite(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	// Banned users cannot rejoin via invite. Check before processing the code
	// so no instance data is leaked in the error response.
	ban, err := h.store.GetActiveBan(r.Context(), userID)
	if err != nil {
		slog.Error("claimInvite: check ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check ban status"})
		return
	}
	if ban != nil {
		resp := map[string]interface{}{
			"error": "You are banned from this instance.",
		}
		if ban.ExpiresAt != nil {
			resp["ban_expires_at"] = ban.ExpiresAt.Format(time.RFC3339)
		}
		writeJSON(w, http.StatusForbidden, resp)
		return
	}
	var req claimInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
		return
	}
	inv, err := h.store.GetInviteByCode(r.Context(), req.Code)
	if err != nil {
		slog.Error("get invite by code", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate invite"})
		return
	}
	if inv == nil || time.Now().After(inv.ExpiresAt) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired invite code"})
		return
	}
	claimed, err := h.store.ClaimInviteUse(r.Context(), req.Code)
	if err != nil {
		slog.Error("claim invite use", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to claim invite"})
		return
	}
	if !claimed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invite code has reached maximum uses"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
