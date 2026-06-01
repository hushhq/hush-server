//go:build e2e_test

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/hushhq/hush-server/internal/api"
	"github.com/hushhq/hush-server/internal/config"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/server"
)

const (
	setupTimeout   = 45 * time.Second // vite-node cold start + WASM init + session
	commandTimeout = 20 * time.Second
	epochTimeout   = 20 * time.Second
)

// TestVoiceConvergence_TwoClients_ThroughRealServer drives two genuinely
// distinct hush-web clients (own process, own WASM, own IndexedDB) through one
// real server and asserts their converged final state. It is the regression
// gate for HUSHHQ-104 and HUSHHQ-105.
//
// CORE-INVARIANTS: MLS, Messages, and Realtime Catch-up (epoch convergence,
// frame-key derivation, eviction key rotation).
//
// Scenarios, all asserted on observable converged state (never on "a method was
// called"):
//   - 104: creator (epoch 0) + external joiner (epoch 1) converge to the SAME
//     epoch and SAME frame key; a payload from one decrypts for the other in
//     both directions. Explicitly rejects the 104 signature (creator stuck 0).
//   - 105a: removing a hyphenated `userId:deviceId` identity completes with no
//     base64 decode error.
//   - 105b: after removing the joiner the remover advances to the new epoch with
//     a fresh frame key via its own local merge (the server echoes the remover's
//     own commit and the client skips it, so a delivery shortcut cannot satisfy
//     this).
//   - eviction forward secrecy: the removed member cannot decrypt a payload
//     produced at the post-removal epoch.
func TestVoiceConvergence_TwoClients_ThroughRealServer(t *testing.T) {
	dbURL := testDBURL()
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL not set; skipping voice convergence e2e")
	}
	webDir := os.Getenv("HUSH_WEB_DIR")
	if webDir == "" {
		t.Skip("HUSH_WEB_DIR not set; skipping voice convergence e2e")
	}
	requireHarness(t, webDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	migrateUp(t, dbURL)
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer pool.Close()

	ts := httptest.NewServer(buildTestHandler(t, pool))
	defer ts.Close()
	baseURL := ts.URL
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// Seed a real server + voice channel. Channel membership is server
	// membership (db.IsChannelMember), and the WS `subscribe` frame is
	// membership-gated, so both users must be members for the real fan-out path
	// to deliver commits. This keeps the harness on the genuine subscribe path.
	serverRow, err := pool.CreateServer(ctx, []byte("e2e"))
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	channelRow, err := pool.CreateChannel(ctx, serverRow.ID, []byte("e2e"), "voice", nil, 0)
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	channelID := channelRow.ID

	creator := startHarnessClient(t, ctx, webDir, "creator", baseURL, wsURL, channelID)
	defer creator.stop()
	joiner := startHarnessClient(t, ctx, webDir, "joiner", baseURL, wsURL, channelID)
	defer joiner.stop()

	creatorSetup := creator.await("setup_done", setupTimeout)
	joinerSetup := joiner.await("setup_done", setupTimeout)

	addMember(t, ctx, pool, serverRow.ID, evStr(t, creatorSetup, "userId"))
	addMember(t, ctx, pool, serverRow.ID, evStr(t, joinerSetup, "userId"))

	// Start the voice MLS session (subscribes to the channel WS topic). This is
	// the exact subscription whose absence caused HUSHHQ-104.
	creator.send(cmd("start"))
	creator.await("started", commandTimeout)
	joiner.send(cmd("start"))
	joiner.await("started", commandTimeout)
	// Let the server register both subscriptions before any commit is broadcast.
	time.Sleep(500 * time.Millisecond)

	// --- HUSHHQ-104: external-join convergence ---
	creator.send(cmd("create"))
	created := creator.await("created", commandTimeout)
	if got := evInt(t, created, "epoch"); got != 0 {
		t.Fatalf("creator initial epoch = %d, want 0", got)
	}

	joiner.send(cmd("join"))
	joined := joiner.await("joined", commandTimeout)
	joinerEpoch := evInt(t, joined, "epoch")
	joinerHash := evStr(t, joined, "frameKeyHash")
	if joinerEpoch != 1 {
		t.Fatalf("joiner epoch after external join = %d, want 1", joinerEpoch)
	}

	creator.send(map[string]any{"cmd": "await_epoch", "epoch": 1})
	converged := creator.await("epoch_reached", epochTimeout)
	creatorEpoch := evInt(t, converged, "epoch")
	creatorHash := evStr(t, converged, "frameKeyHash")

	// Converged final state: same epoch, same frame key.
	if creatorEpoch != joinerEpoch {
		t.Fatalf("epoch divergence: creator=%d joiner=%d", creatorEpoch, joinerEpoch)
	}
	if creatorHash != joinerHash {
		t.Fatalf("frame-key divergence at epoch %d: creator=%s joiner=%s", creatorEpoch, creatorHash, joinerHash)
	}
	// Reject the exact 104 signature (creator stuck at 0 while joiner at 1).
	if creatorEpoch == 0 {
		t.Fatalf("HUSHHQ-104 regression: creator never advanced past epoch 0")
	}

	// --- bidirectional decrypt across two independent processes' keys ---
	assertCrossDecrypt(t, creator, joiner, "hello-from-creator")
	assertCrossDecrypt(t, joiner, creator, "hello-from-joiner")

	// --- HUSHHQ-105a + 105b: eviction identity encoding + local merge ---
	joinerIdentity := evStr(t, joinerSetup, "userId") + ":" + evStr(t, joinerSetup, "deviceId")
	creator.send(map[string]any{"cmd": "remove", "identity": joinerIdentity})
	removed := creator.await("removed", commandTimeout) // 105a: no base64 decode error
	removedEpoch := evInt(t, removed, "epoch")
	removedHash := evStr(t, removed, "frameKeyHash")
	if removedEpoch <= creatorEpoch {
		t.Fatalf("HUSHHQ-105b regression: epoch did not advance on removal (was %d, now %d)", creatorEpoch, removedEpoch)
	}
	if removedHash == creatorHash {
		t.Fatalf("HUSHHQ-105b regression: frame key unchanged after eviction (epoch %d)", removedEpoch)
	}

	// --- eviction forward secrecy: removed member cannot decrypt new epoch ---
	nonce := randNonceHex(t)
	creator.send(map[string]any{"cmd": "encrypt", "nonceHex": nonce, "plaintext": "post-eviction"})
	postEvict := creator.await("ciphertext", commandTimeout)
	joiner.send(map[string]any{"cmd": "decrypt", "nonceHex": nonce, "b64": evStr(t, postEvict, "b64")})
	evicted := joiner.await("decrypted", commandTimeout)
	if evBool(t, evicted, "ok") {
		t.Fatalf("eviction forward-secrecy violation: removed member decrypted epoch-%d payload", removedEpoch)
	}
}

// assertCrossDecrypt has `from` encrypt a probe under its frame key and `to`
// decrypt it under its own, proving both converged on identical key material
// usable for voice E2EE.
func assertCrossDecrypt(t *testing.T, from, to *harnessClient, plaintext string) {
	t.Helper()
	nonce := randNonceHex(t)
	from.send(map[string]any{"cmd": "encrypt", "nonceHex": nonce, "plaintext": plaintext})
	ct := from.await("ciphertext", commandTimeout)
	to.send(map[string]any{"cmd": "decrypt", "nonceHex": nonce, "b64": evStr(t, ct, "b64")})
	res := to.await("decrypted", commandTimeout)
	if !evBool(t, res, "ok") {
		t.Fatalf("%s could not decrypt %s's payload: %v", to.role, from.role, res)
	}
	if got := evStr(t, res, "plaintext"); got != plaintext {
		t.Fatalf("cross-decrypt plaintext mismatch: got %q want %q", got, plaintext)
	}
}

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

func testDBURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return os.Getenv("DATABASE_URL")
}

func requireHarness(t *testing.T, webDir string) {
	t.Helper()
	for _, rel := range []string{
		filepath.Join("e2e", "harness", "client.mjs"),
		filepath.Join("node_modules", ".bin", "vite-node"),
	} {
		if _, err := os.Stat(filepath.Join(webDir, rel)); err != nil {
			t.Skipf("harness prerequisite missing (%s); run `npm ci` in HUSH_WEB_DIR: %v", rel, err)
		}
	}
}

func migrateUp(t *testing.T, url string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
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

func buildTestHandler(t *testing.T, pool *db.Pool) http.Handler {
	t.Helper()
	cfg := config.Config{
		JWTSecret:  "e2e-test-secret",
		JWTExpiry:  time.Hour,
		CORSOrigin: "*", // allow the non-browser Node WS client (no Origin header)
		Production: false,
	}
	handler, _ := server.BuildServer(server.Deps{
		Cfg:            cfg,
		Pool:           pool,
		HandshakeCache: api.NewInstanceCache(),
		HTTPMetrics:    api.NewHTTPMetrics(),
		StartedAt:      time.Now(),
	})
	return handler
}

func addMember(t *testing.T, ctx context.Context, pool *db.Pool, serverID, userID string) {
	t.Helper()
	if err := pool.AddServerMember(ctx, serverID, userID, 0); err != nil {
		t.Fatalf("AddServerMember(%s): %v", userID, err)
	}
}

// ---------------------------------------------------------------------------
// Event field accessors (JSON numbers decode as float64)
// ---------------------------------------------------------------------------

func cmd(name string) map[string]any { return map[string]any{"cmd": name} }

func evInt(t *testing.T, ev map[string]any, key string) int {
	t.Helper()
	v, ok := ev[key].(float64)
	if !ok {
		t.Fatalf("event field %q not a number: %v", key, ev)
	}
	return int(v)
}

func evStr(t *testing.T, ev map[string]any, key string) string {
	t.Helper()
	v, ok := ev[key].(string)
	if !ok {
		t.Fatalf("event field %q not a string: %v", key, ev)
	}
	return v
}

func evBool(t *testing.T, ev map[string]any, key string) bool {
	t.Helper()
	v, ok := ev[key].(bool)
	if !ok {
		t.Fatalf("event field %q not a bool: %v", key, ev)
	}
	return v
}

func randNonceHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand nonce: %v", err)
	}
	return hex.EncodeToString(b)
}
