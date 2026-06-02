//go:build e2e_test

package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/db"
)

// registerTestRoutes mounts the E2E-only bootstrap endpoints (session + seed).
// Compiled in ONLY under -tags e2e_test; production builds use the no-op in
// test_routes_prod.go, so /api/test/* cannot exist in a shipped binary.
//
// Security: the client's BIP39 identity key is generated client-side and never
// reaches the server, so these endpoints cannot weaken E2EE. The only risk they
// gate is unauthenticated account/server creation, which is why they are
// build-tagged out of production rather than guarded by an env var.
func registerTestRoutes(r chi.Router, pool *db.Pool, jwtSecret string) {
	if pool == nil || jwtSecret == "" {
		return
	}
	h := &testHandler{pool: pool, jwtSecret: jwtSecret}
	r.Post("/api/test/session", h.createSession)
	r.Post("/api/test/seed", h.createSeed)
}

type testHandler struct {
	pool      *db.Pool
	jwtSecret string
}

type testSessionRequest struct {
	Username  string `json:"username"`
	PublicKey string `json:"publicKey"` // base64 Ed25519 public key (32 bytes)
}

type testSessionResponse struct {
	Token    string `json:"token"`
	UserID   string `json:"userId"`
	DeviceID string `json:"deviceId"`
}

// createSession provisions a fully authenticatable session (user row + session
// row + active device key) the same way the real register/verify flow does, so
// the issued JWT passes RequireAuth (session-by-token-hash + IsDeviceActive).
func (h *testHandler) createSession(w http.ResponseWriter, r *http.Request) {
	var req testSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeTestJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	pubKey, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(pubKey) != 32 {
		writeTestJSON(w, http.StatusBadRequest, map[string]string{"error": "publicKey must be base64 Ed25519 (32 bytes)"})
		return
	}
	username := req.Username
	if username == "" {
		username = "e2e-" + uuid.New().String()[:8]
	}

	ctx := r.Context()
	user, err := h.pool.CreateUserWithPublicKey(ctx, username, username, pubKey)
	if err != nil {
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "create user: " + err.Error()})
		return
	}

	deviceID := uuid.New().String()
	sessionID := uuid.New().String()
	expiresAt := time.Now().Add(1 * time.Hour)

	token, err := auth.SignJWT(user.ID, sessionID, deviceID, h.jwtSecret, expiresAt)
	if err != nil {
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "sign jwt: " + err.Error()})
		return
	}
	if _, err := h.pool.CreateSession(ctx, sessionID, user.ID, auth.TokenHash(token), expiresAt); err != nil {
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "create session: " + err.Error()})
		return
	}
	if _, err := h.pool.BackfillRootDeviceKey(ctx, user.ID, deviceID, "e2e-test-device", pubKey); err != nil {
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "register device: " + err.Error()})
		return
	}

	writeTestJSON(w, http.StatusCreated, testSessionResponse{Token: token, UserID: user.ID, DeviceID: deviceID})
}

type testSeedRequest struct {
	UserIDs []string `json:"userIds"`
}

type testSeedResponse struct {
	ServerID  string `json:"serverId"`
	ChannelID string `json:"channelId"`
}

// createSeed provisions one server, one voice channel, and a membership row per
// userId, mirroring the direct-DB seeding the headless Go harness performs. It
// exists so the Playwright media suite, which has no DB handle, can seed the
// shared voice channel over HTTP. E2E only: compiled in solely under
// -tags e2e_test.
func (h *testHandler) createSeed(w http.ResponseWriter, r *http.Request) {
	var req testSeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.UserIDs) == 0 {
		writeTestJSON(w, http.StatusBadRequest, map[string]string{"error": "userIds required"})
		return
	}
	ctx := r.Context()
	srv, err := h.pool.CreateServer(ctx, []byte("e2e"))
	if err != nil {
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "create server: " + err.Error()})
		return
	}
	ch, err := h.pool.CreateChannel(ctx, srv.ID, []byte("e2e"), "voice", nil, 0)
	if err != nil {
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "create channel: " + err.Error()})
		return
	}
	for _, uid := range req.UserIDs {
		if err := h.pool.AddServerMember(ctx, srv.ID, uid, 0); err != nil {
			writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "add member " + uid + ": " + err.Error()})
			return
		}
	}
	writeTestJSON(w, http.StatusCreated, testSeedResponse{ServerID: srv.ID, ChannelID: ch.ID})
}

func writeTestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
