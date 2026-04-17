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
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/transparency"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	maxUsernameLen           = 128
	maxDisplayLen            = 128
	nonceTTL                 = 60 * time.Second
	noncePurgeInterval       = 5 * time.Minute
	noncePurgeContextSecs    = 30
	usernameCheckTimeout     = 5 * time.Second
	ed25519PublicKeyLen      = 32
	defaultGuestSessionHours = 1
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// AuthRoutes returns the chi router for /api/auth.
// It also starts a background goroutine that purges expired auth nonces every
// noncePurgeInterval. The goroutine has no shutdown signal - process exit
// terminates it, consistent with the system_messages cleanup pattern.
//
// transparencySvc may be nil when the transparency log is not configured for
// this instance. All transparency operations nil-guard before use.
//
// hub may be nil; all broadcast calls within device handlers are nil-checked
// before use. Pass the *ws.Hub from router setup for WS notifications.
//
// Guest session duration is read from GUEST_SESSION_HOURS (default 1 hour).
func AuthRoutes(store db.Store, jwtSecret string, jwtExpiry time.Duration, transparencySvc *transparency.TransparencyService, hub ...GlobalBroadcaster) chi.Router {
	guestHours := defaultGuestSessionHours
	if h := os.Getenv("GUEST_SESSION_HOURS"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 {
			guestHours = v
		}
	}
	guestSessionDuration := time.Duration(guestHours) * time.Hour

	r := chi.NewRouter()
	h := &authHandler{
		store:                store,
		jwtSecret:            jwtSecret,
		jwtExpiry:            jwtExpiry,
		guestSessionDuration: guestSessionDuration,
		transparencySvc:      transparencySvc,
	}

	r.Post("/register", h.register)
	r.Post("/guest", h.guestAuth)
	r.Get("/check-username/{username}", h.checkUsername)
	r.Post("/challenge", h.challenge)
	r.Post("/verify", h.verify)
	r.Post("/federated-verify", h.federatedVerify)
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(jwtSecret, store))
		r.Post("/logout", h.logout)
		r.Get("/me", h.me)
	})

	// Device management and multi-device linking (require auth - mounted inline).
	// Hub is variadic to maintain backward compatibility with existing call sites
	// (tests and main.go) that omit the hub parameter. Nil is safe; all device
	// handler broadcast calls nil-check before use.
	var deviceHub GlobalBroadcaster
	if len(hub) > 0 {
		deviceHub = hub[0]
	}
	r.Group(DeviceRoutes(store, jwtSecret, transparencySvc, deviceHub))

	go h.purgeNoncesLoop()

	return r
}

type authHandler struct {
	store                db.Store
	jwtSecret            string
	jwtExpiry            time.Duration
	guestSessionDuration time.Duration
	transparencySvc      *transparency.TransparencyService
}

// ensureVerifiedDeviceRegistered backfills the current device into the device
// registry after a successful challenge-response login. This keeps older
// accounts, created before device-key tracking existed, compatible with flows
// such as multi-device linking that need the approving device to have a stored key.
//
// The operation is intentionally best-effort: authentication must not fail just
// because auxiliary device metadata could not be upserted.
func (h *authHandler) ensureVerifiedDeviceRegistered(ctx context.Context, userID, deviceID string, publicKey []byte) {
	if strings.TrimSpace(deviceID) == "" {
		return
	}

	if err := h.store.InsertDeviceKey(ctx, userID, deviceID, "", publicKey, nil); err != nil {
		slog.Warn("backfill device key on verify", "user_id", userID, "device_id", deviceID, "err", err)
		return
	}
	if err := h.store.UpsertDevice(ctx, userID, deviceID, ""); err != nil {
		slog.Warn("backfill device row on verify", "user_id", userID, "device_id", deviceID, "err", err)
	}
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
// Accepts {username, displayName, publicKey (base64)} - no password.
// On success returns JWT + user object.
func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Label = strings.TrimSpace(req.Label)
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

	// Probe for existing user with this public key - account recovery detection.
	// GetUserByPublicKey error (e.g. DB issue) is fail-open: fall through to
	// CreateUserWithPublicKey which will either succeed or return its own error.
	if existingUser, _ := h.store.GetUserByPublicKey(r.Context(), publicKeyBytes); existingUser != nil {
		// Account recovery: same root public key re-registering (new device, lost device).
		// This is the highest-risk operation per CONTEXT.md; log synchronously.
		if h.transparencySvc != nil {
			entry := &transparency.LogEntry{
				OperationType: transparency.OpAccountRecovery,
				UserPublicKey: publicKeyBytes,
				Timestamp:     time.Now().Unix(),
			}
			if logErr := h.transparencySvc.AppendAndNotify(r.Context(), entry, existingUser.ID); logErr != nil {
				slog.Error("transparency: append account_recovery entry", "err", logErr, "user_id", existingUser.ID)
			}
		}
		// Re-insert device key for the new device (ON CONFLICT DO UPDATE - idempotent).
		deviceID := req.DeviceID
		if deviceID == "" {
			deviceID = uuid.New().String()
		}
		if err := h.store.InsertDeviceKey(r.Context(), existingUser.ID, deviceID, req.Label, publicKeyBytes, nil); err != nil {
			slog.Error("insert device key on account recovery", "err", err)
		}
		h.sendAuthResponse(w, r, existingUser, deviceID)
		return
	}

	// Block registration if the username belongs to a banned user (IROLE-03).
	if existing, _ := h.store.GetUserByUsername(r.Context(), req.Username); existing != nil {
		if ban, _ := h.store.GetActiveInstanceBan(r.Context(), existing.ID); ban != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Registration blocked. Reason: " + ban.Reason})
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
	if err := h.store.InsertDeviceKey(r.Context(), user.ID, deviceID, req.Label, publicKeyBytes, nil); err != nil {
		slog.Error("insert device key on register", "err", err)
		// Non-fatal: device key is supplementary metadata; proceed with auth.
	}

	// Append transparency log entry synchronously before responding.
	// On failure, log the error but still return 201 - the user was created
	// successfully. Log failure is non-fatal per Pitfall 4 in RESEARCH.md.
	if h.transparencySvc != nil {
		entry := &transparency.LogEntry{
			OperationType: transparency.OpRegister,
			UserPublicKey: publicKeyBytes,
			Timestamp:     time.Now().Unix(),
			// UserSignature is nil in MVP mode; Plan 03 adds client-side signing.
			// transparency_sig from req body (future field) will be wired in Plan 03.
		}
		if logErr := h.transparencySvc.AppendAndNotify(r.Context(), entry, user.ID); logErr != nil {
			slog.Error("transparency: append register entry", "err", logErr, "user_id", user.ID)
		}
	}

	h.sendAuthResponse(w, r, user, deviceID)
}

// guestAuth handles POST /api/auth/guest.
// Issues a short-lived JWT for an ephemeral guest session. No user record is
// created or persisted. The JWT carries is_guest=true so the middleware skips
// DB session validation. Guests may view channels and send messages in open
// channels; write operations that require a persisted identity (guild creation,
// device management) are blocked by checking IsGuest on the request context.
//
// Returns: { token, guestId, expiresAt }
func (h *authHandler) guestAuth(w http.ResponseWriter, r *http.Request) {
	// Generate an ephemeral Ed25519 keypair for the guest identity display name.
	// The public key is returned to the client as an opaque identifier - it is
	// NOT stored in the database and cannot be used for account recovery.
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		slog.Error("guest auth: generate ephemeral keypair", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create guest session"})
		return
	}

	// Derive a stable guest ID from the first 8 bytes of the public key so the
	// client has a usable identifier for the lifetime of the session.
	guestID := "guest_" + base64.RawURLEncoding.EncodeToString(pubKey[:8])
	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(h.guestSessionDuration)

	tokenString, err := auth.SignGuestJWT(guestID, sessionID, h.jwtSecret, expiresAt)
	if err != nil {
		slog.Error("guest auth: sign jwt", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create guest session"})
		return
	}

	writeJSON(w, http.StatusOK, models.GuestAuthResponse{
		Token:     tokenString,
		GuestID:   guestID,
		ExpiresAt: expiresAt,
	})
}

// challenge handles POST /api/auth/challenge.
// Returns a 60-second nonce for the client to sign. Does not check key existence
// here - existence is verified at /verify to prevent key-enumeration timing attacks.
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
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authentication failed"})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown public key"})
		return
	}

	ban, _ := h.store.GetActiveInstanceBan(r.Context(), user.ID)
	if ban != nil {
		msg := "Account banned: " + ban.Reason
		writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
		return
	}

	h.ensureVerifiedDeviceRegistered(r.Context(), user.ID, req.DeviceID, publicKeyBytes)
	h.sendAuthResponse(w, r, user, req.DeviceID)

	// Fire-and-forget: update device last_seen. Prefer the caller-supplied
	// device ID so linked devices with non-root device public keys still track
	// activity correctly. Fall back to the legacy public-key scan when older
	// clients omit deviceId.
	go func(deviceID string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if deviceID != "" {
			_ = h.store.UpdateDeviceLastSeen(ctx, user.ID, deviceID)
			return
		}
		// Best-effort legacy fallback: look up a device matching this public key.
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
	}(req.DeviceID)
}

// federatedAuthResponse is returned by POST /api/auth/federated-verify.
type federatedAuthResponse struct {
	Token             string                    `json:"token"`
	FederatedIdentity *models.FederatedIdentity `json:"federatedIdentity"`
}

// federatedVerify handles POST /api/auth/federated-verify.
// Authenticates a user from a foreign instance via Ed25519 challenge-response
// and issues a stateless federated JWT. No DB session record is created.
func (h *authHandler) federatedVerify(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "federation is not supported in this MVP"})
}

func (h *authHandler) sendAuthResponse(w http.ResponseWriter, r *http.Request, user *models.User, deviceID string) {
	expiresAt := time.Now().Add(h.jwtExpiry)
	sessionID := uuid.New().String()
	tokenString, err := auth.SignJWT(user.ID, sessionID, deviceID, h.jwtSecret, expiresAt)
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

// checkUsername handles GET /api/auth/check-username/{username}.
// Returns {"available": true/false}. Public endpoint - no auth required.
func (h *authHandler) checkUsername(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(chi.URLParam(r, "username"))
	if err := validateUsername(username); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), usernameCheckTimeout)
	defer cancel()

	_, err := h.store.GetUserByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]bool{"available": true})
		return
	}
	if err != nil {
		slog.Error("check username", "username", username, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "username check failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"available": false})
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
