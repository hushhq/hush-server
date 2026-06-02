//go:build e2e_test

package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/hushhq/hush-server/internal/db"
)

// TestSeedEndpoint_CreatesVoiceChannelAndMemberships exercises the build-tagged
// POST /api/test/seed over the real BuildServer router. The Playwright media
// suite (HUSHHQ-107) has no DB handle, so it seeds the shared voice channel
// through this endpoint; this test is the Go-side guard that the endpoint
// actually provisions a server, a voice channel, and one membership per user.
//
// It asserts the OUTCOME (both users are members of the returned channel via the
// real db.IsChannelMember read), not merely that the handler returned 201.
func TestSeedEndpoint_CreatesVoiceChannelAndMemberships(t *testing.T) {
	dbURL := testDBURL()
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL not set; skipping seed endpoint e2e")
	}

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

	uidA := seedTestUser(t, ctx, pool)
	uidB := seedTestUser(t, ctx, pool)

	body, _ := json.Marshal(map[string]any{"userIds": []string{uidA, uidB}})
	res, err := http.Post(ts.URL+"/api/test/seed", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/test/seed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("seed: want 201, got %d", res.StatusCode)
	}

	var resp struct {
		ServerID  string `json:"serverId"`
		ChannelID string `json:"channelId"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode seed response: %v", err)
	}
	if resp.ServerID == "" || resp.ChannelID == "" {
		t.Fatalf("seed returned empty ids: %+v", resp)
	}

	for _, uid := range []string{uidA, uidB} {
		member, err := pool.IsChannelMember(ctx, resp.ChannelID, uid)
		if err != nil {
			t.Fatalf("IsChannelMember(%s): %v", uid, err)
		}
		if !member {
			t.Fatalf("user %s is not a member of seeded channel %s", uid, resp.ChannelID)
		}
	}
}

// TestSeedEndpoint_RejectsEmptyUserIDs proves the endpoint fails fast on a body
// with no userIds rather than provisioning a memberless server.
func TestSeedEndpoint_RejectsEmptyUserIDs(t *testing.T) {
	dbURL := testDBURL()
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL not set; skipping seed endpoint e2e")
	}

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

	body, _ := json.Marshal(map[string]any{"userIds": []string{}})
	res, err := http.Post(ts.URL+"/api/test/seed", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/test/seed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("seed empty userIds: want 400, got %d", res.StatusCode)
	}
}

// seedTestUser provisions one user row with a fresh random Ed25519 public key
// (users.root_public_key is UNIQUE) and returns its id. The seed endpoint only
// needs an existing userId to attach a membership; the full session/JWT flow is
// covered by the voice convergence e2e.
func seedTestUser(t *testing.T, ctx context.Context, pool *db.Pool) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	name := "e2e-seed-" + uuid.New().String()[:8]
	user, err := pool.CreateUserWithPublicKey(ctx, name, name, pub)
	if err != nil {
		t.Fatalf("CreateUserWithPublicKey: %v", err)
	}
	return user.ID
}
