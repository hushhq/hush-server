package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	maxUsernameLen         = 128
	maxDisplayLen          = 128
	nonceTTL               = 60 * time.Second
	noncePurgeInterval     = 5 * time.Minute
	noncePurgeContextSecs  = 30
	ed25519PublicKeyLen     = 32
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// AuthRoutes returns the chi router for /api/auth.
// It also starts a background goroutine that purges expired auth nonces every
// noncePurgeInterval. The goroutine has no shutdown signal — process exit
// terminates it, consistent with the system_messages cleanup pattern.
func AuthRoutes(store db.Store, jwtSecret string, jwtExpiry time.Duration) chi.Router {
	r := chi.NewRouter()
	h := &authHandler{store: store, jwtSecret: jwtSecret, jwtExpiry: jwtExpiry}

	r.Post("/register", h.register)
	r.Post("/challenge", h.challenge)
	r.Post("/verify", h.verify)
	r.Post("/guest", h.guest)
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(jwtSecret, store))
		r.Post("/logout", h.logout)
		r.Get("/me", h.me)
	})

	// Device management and multi-device linking (require auth — mounted inline).
	r.Group(DeviceRoutes(store, jwtSecret))

	go h.purgeNoncesLoop()

	return r
}

type authHandler struct {
	store     db.Store
	jwtSecret string
	jwtExpiry time.Duration
}

// purgeNoncesLoop runs PurgeExpiredNonces every noncePurgeInterval.
// Uses a 30-second context timeout per tick to prevent leaked connections.
func (h *authHandler) purgeNoncesLoop() {
	ticker := time.NewTicker(noncePurgeInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), noncePurgeContextSecs*time.Second)
		n, err := h.store.PurgeExpiredNonces(ctx)
		cancel()
		if err != nil {
			slog.Error("purge expired nonces", "err", err)
		} else if n > 0 {
			slog.Info("purged expired auth nonces", "count", n)
		}
	}
}

// register handles POST /api/auth/register.
// Accepts {username, displayName, publicKey (base64)} — no password.
// On success returns JWT + user object.
func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	if err := validateUsername(req.Username); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid publicKey encoding"})
		return
	}
	if len(publicKeyBytes) != ed25519PublicKeyLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "publicKey must be 32 bytes (Ed25519)"})
		return
	}

	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("get instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "registration failed"})
		return
	}
	switch cfg.RegistrationMode {
	case "closed":
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Registration is closed"})
		return
	case "invite_only":
		if req.InviteCode == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "An invite code is required"})
			return
		}
		invite, err := h.store.GetInviteByCode(r.Context(), req.InviteCode)
		if err != nil || invite == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Invalid invite code"})
			return
		}
		ok, err := h.store.ClaimInviteUse(r.Context(), req.InviteCode)
		if err != nil || !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Invite code has expired or reached its use limit"})
			return
		}
	}

	user, err := h.store.CreateUserWithPublicKey(r.Context(), req.Username, req.DisplayName, publicKeyBytes)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "duplicate key") || strings.Contains(errStr, "unique constraint") {
			if strings.Contains(errStr, "root_public_key") {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "Public key already registered"})
				return
			}
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Username already taken"})
			return
		}
		slog.Error("create user with public key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "registration failed"})
		return
	}

	deviceID := req.DeviceID
	if deviceID == "" {
		deviceID = uuid.New().String()
	}
	if err := h.store.InsertDeviceKey(r.Context(), user.ID, deviceID, publicKeyBytes, nil); err != nil {
		slog.Error("insert device key on register", "err", err)
		// Non-fatal: device key is supplementary metadata; proceed with auth.
	}

	h.sendAuthResponse(w, r, user)
}

// challenge handles POST /api/auth/challenge.
// Returns a 60-second nonce for the client to sign. Does not check key existence
// here — existence is verified at /verify to prevent key-enumeration timing attacks.
func (h *authHandler) challenge(w http.ResponseWriter, r *http.Request) {
	var req models.ChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(publicKeyBytes) != ed25519PublicKeyLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid publicKey"})
		return
	}

	nonce, err := auth.GenerateNonce()
	if err != nil {
		slog.Error("generate nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate challenge"})
		return
	}

	expiresAt := time.Now().Add(nonceTTL)
	if err := h.store.InsertAuthNonce(r.Context(), nonce, publicKeyBytes, expiresAt); err != nil {
		slog.Error("insert auth nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate challenge"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"nonce": nonce})
}

// verify handles POST /api/auth/verify.
// Consumes the nonce atomically, verifies the Ed25519 signature, then issues a JWT.
func (h *authHandler) verify(w http.ResponseWriter, r *http.Request) {
	var req models.VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	publicKeyBytes, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(publicKeyBytes) != ed25519PublicKeyLen {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(req.Signature)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	// Atomically delete the nonce and retrieve the stored public key.
	storedKey, err := h.store.ConsumeAuthNonce(r.Context(), req.Nonce)
	if err != nil {
		// ErrNoRows or any error means nonce absent or expired.
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Challenge expired, please try again"})
		return
	}

	// The nonce's stored public key must exactly match the request public key.
	if subtle.ConstantTimeCompare(storedKey, publicKeyBytes) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	if err := auth.VerifySignature(publicKeyBytes, req.Nonce, signatureBytes); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	user, err := h.store.GetUserByPublicKey(r.Context(), publicKeyBytes)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}

	ban, _ := h.store.GetActiveInstanceBan(r.Context(), user.ID)
	if ban != nil {
		msg := "Account banned: " + ban.Reason
		writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
		return
	}

	h.sendAuthResponse(w, r, user)

	// Fire-and-forget: update device last_seen. Device ID is not available without
	// a separate device lookup; skip for now and log only on unexpected errors.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Best-effort: look up a device matching this public key.
		devices, err := h.store.ListDeviceKeys(ctx, user.ID)
		if err != nil {
			return
		}
		for _, d := range devices {
			if subtle.ConstantTimeCompare(d.DevicePublicKey, publicKeyBytes) == 1 {
				_ = h.store.UpdateDeviceLastSeen(ctx, user.ID, d.DeviceID)
				break
			}
		}
	}()
}

// guest handles POST /api/auth/guest.
// Generates an ephemeral Ed25519 keypair server-side, creates a guest user,
// and returns a JWT. The private key is discarded — guest accounts have no
// challenge-response auth; they can only log out. (IDEN-07)
func (h *authHandler) guest(w http.ResponseWriter, r *http.Request) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		slog.Error("generate guest keypair", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "guest creation failed"})
		return
	}

	username := "guest_" + uuid.New().String()[:8]
	user, err := h.store.CreateUserWithPublicKey(r.Context(), username, "Guest", pub)
	if err != nil {
		slog.Error("create guest user", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "guest creation failed"})
		return
	}

	h.sendAuthResponse(w, r, user)
}

func (h *authHandler) sendAuthResponse(w http.ResponseWriter, r *http.Request, user *models.User) {
	expiresAt := time.Now().Add(h.jwtExpiry)
	sessionID := uuid.New().String()
	tokenString, err := auth.SignJWT(user.ID, sessionID, h.jwtSecret, expiresAt)
	if err != nil {
		slog.Error("sign jwt", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	tokenHash := auth.TokenHash(tokenString)
	_, err = h.store.CreateSession(r.Context(), sessionID, user.ID, tokenHash, expiresAt)
	if err != nil {
		slog.Error("create session", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	writeJSON(w, http.StatusOK, models.AuthResponse{Token: tokenString, User: *user})
}

func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromContext(r.Context())
	if sessionID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	if err := h.store.DeleteSessionByID(r.Context(), sessionID); err != nil {
		slog.Error("delete session", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandler) me(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func validateUsername(s string) error {
	if s == "" {
		return errors.New("username is required")
	}
	if len(s) > maxUsernameLen {
		return errors.New("username too long")
	}
	if !usernameRE.MatchString(s) {
		return errors.New("username may only contain letters, numbers, dots, underscores, and hyphens")
	}
	return nil
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
