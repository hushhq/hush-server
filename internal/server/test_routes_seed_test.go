//go:build e2e_test

package server

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/hushhq/hush-server/internal/api"
	"github.com/hushhq/hush-server/internal/config"
	"github.com/hushhq/hush-server/internal/db"
)

// testSeedDBURL returns the database URL from the environment, or empty string
// if none is set. Mirrors the pattern used in internal/e2e.
func testSeedDBURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return os.Getenv("DATABASE_URL")
}

// testSeedMigrateUp runs all pending migrations against the given database URL.
// It mirrors the migrateUp helper in internal/e2e/voice_convergence_test.go,
// adapted for the internal/server package working directory.
func testSeedMigrateUp(t *testing.T, url string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/server is two levels below the repo root; migrations/ lives at root.
	dir := filepath.Join(wd, "..", "..", "migrations")
	m, err := migrate.New("file://"+filepath.ToSlash(dir), url)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
}

// buildSeedTestHandler builds the full server handler with a real pool and the
// e2e JWT secret, identical to the approach in voice_convergence_test.go.
func buildSeedTestHandler(t *testing.T, pool *db.Pool) http.Handler {
	t.Helper()
	cfg := config.Config{
		JWTSecret:  "e2e-test-secret",
		JWTExpiry:  time.Hour,
		CORSOrigin: "*",
		Production: false,
	}
	handler, _ := BuildServer(Deps{
		Cfg:            cfg,
		Pool:           pool,
		HandshakeCache: api.NewInstanceCache(),
		HTTPMetrics:    api.NewHTTPMetrics(),
		StartedAt:      time.Now(),
	})
	return handler
}

// createSeedTestSession provisions a user via POST /api/test/session and
// returns the userId from the response. This avoids any direct DB call for user
// creation from the test, keeping both helpers (session + seed) exercised
// together.
func createSeedTestSession(t *testing.T, handler http.Handler, pubKeyB64 string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"publicKey": pubKeyB64,
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/test/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/test/session = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	uid, ok := resp["userId"].(string)
	if !ok || uid == "" {
		t.Fatalf("session response missing userId: %v", resp)
	}
	return uid
}

// TestTestSeedRoute_CreatesServerChannelMemberships is an e2e-tagged integration
// test. It spins up a real server handler backed by a real database (skipped when
// TEST_DATABASE_URL/DATABASE_URL is unset), provisions two users via the session
// endpoint, then posts to /api/test/seed and asserts a 201 response with non-empty
// serverId and channelId.
//
// CORE-INVARIANTS: Members/Profiles (server membership provisioned correctly).
func TestTestSeedRoute_CreatesServerChannelMemberships(t *testing.T) {
	dbURL := testSeedDBURL()
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL not set; skipping seed route integration test")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testSeedMigrateUp(t, dbURL)

	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer pool.Close()

	handler := buildSeedTestHandler(t, pool)

	// Generate two random 32-byte Ed25519 public keys per run so repeated runs
	// against a persistent database do not collide on the UNIQUE root_public_key
	// constraint. The server validates only length (32 bytes) and base64 encoding;
	// it does not verify the key is a valid curve point.
	newPubKeyB64 := func() string {
		var raw [32]byte
		if _, err := crand.Read(raw[:]); err != nil {
			t.Fatalf("crypto/rand.Read: %v", err)
		}
		return base64.StdEncoding.EncodeToString(raw[:])
	}

	uidA := createSeedTestSession(t, handler, newPubKeyB64())
	uidB := createSeedTestSession(t, handler, newPubKeyB64())

	// POST /api/test/seed with both user IDs.
	reqBody, err := json.Marshal(map[string]any{
		"userIds": []string{uidA, uidB},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/test/seed", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/test/seed = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode seed response: %v", err)
	}
	serverID, ok := resp["serverId"].(string)
	if !ok || serverID == "" {
		t.Fatalf("seed response missing or empty serverId: %v", resp)
	}
	channelID, ok := resp["channelId"].(string)
	if !ok || channelID == "" {
		t.Fatalf("seed response missing or empty channelId: %v", resp)
	}
}
