//go:build e2e_test

package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/db"
)

// TestChatDelivery_TwoClients_ThroughRealServer drives two genuinely distinct
// hush-web clients (own process each) through one real server and asserts that a
// text message round-trips A->B and B->A as the exact plaintext, plus the
// HUSHHQ-105 member-identity encoding parity on the text removal path.
//
// CORE-INVARIANTS: MLS, Messages, and Realtime Catch-up (text group convergence,
// message encrypt/decrypt, channel WS delivery).
//
// Scope: e2e:chat-delivery-headless. PROVES real-server + real WS + real text MLS
// group + real encryptMessage/decryptMessage + real message.send/message.new
// delivery. Does NOT prove React rendering, message-list UI, notifications, or
// browser behaviour (that is the deferred Playwright media/UI milestone).
//
// Pass condition is the OBSERVED plaintext only: B must decrypt exactly what A
// sent, and vice-versa, through the normal receive/decrypt path. message.send /
// message.new / decryptMessage firing are diagnostics, never the pass condition.
func TestChatDelivery_TwoClients_ThroughRealServer(t *testing.T) {
	dbURL := testDBURL()
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL not set; skipping chat delivery e2e")
	}
	webDir := os.Getenv("HUSH_WEB_DIR")
	if webDir == "" {
		t.Skip("HUSH_WEB_DIR not set; skipping chat delivery e2e")
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

	// Seed a real server + TEXT channel + memberships so the membership-gated WS
	// subscribe and message.send paths are exercised for real.
	serverRow, err := pool.CreateServer(ctx, []byte("e2e"))
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	channelRow, err := pool.CreateChannel(ctx, serverRow.ID, []byte("e2e"), "text", nil, 0)
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

	// Subscribe both to the channel topic before any commit/message is sent.
	creator.send(cmd("text_start"))
	creator.await("text_started", commandTimeout)
	joiner.send(cmd("text_start"))
	joiner.await("text_started", commandTimeout)
	time.Sleep(500 * time.Millisecond)

	// Establish the text MLS group: creator (epoch 0) + external joiner (epoch 1);
	// the creator converges to epoch 1 by processing the joiner's commit over WS.
	creator.send(cmd("text_create"))
	creator.await("text_created", commandTimeout)
	joiner.send(cmd("text_join"))
	joined := joiner.await("text_joined", commandTimeout)
	if got := evInt(t, joined, "epoch"); got != 1 {
		t.Fatalf("joiner text epoch after join = %d, want 1", got)
	}
	creator.send(map[string]any{"cmd": "text_await_epoch", "epoch": 1})
	converged := creator.await("text_epoch_reached", epochTimeout)
	if got := evInt(t, converged, "epoch"); got != 1 {
		t.Fatalf("creator did not converge to text epoch 1, got %d", got)
	}

	// A -> B delivery (observed plaintext is the only pass condition).
	msgA := "e2e-chat-" + randNonceHex(t)
	creator.send(map[string]any{"cmd": "send_text", "plaintext": msgA})
	creator.await("text_sent", commandTimeout)
	recvA := joiner.await("text_received", commandTimeout)
	if got := evStr(t, recvA, "plaintext"); got != msgA {
		t.Fatalf("B received %q, want %q", got, msgA)
	}

	// B -> A delivery.
	msgB := "e2e-reply-" + randNonceHex(t)
	joiner.send(map[string]any{"cmd": "send_text", "plaintext": msgB})
	joiner.await("text_sent", commandTimeout)
	recvB := creator.await("text_received", commandTimeout)
	if got := evStr(t, recvB, "plaintext"); got != msgB {
		t.Fatalf("A received %q, want %q", got, msgB)
	}

	// HUSHHQ-105 text identity-encoding parity: removing a hyphenated
	// userId:deviceId via removeMemberFromChannel must complete with no base64
	// decode error and advance the remover's epoch via its local merge.
	joinerIdentity := evStr(t, joinerSetup, "userId") + ":" + evStr(t, joinerSetup, "deviceId")
	creator.send(map[string]any{"cmd": "text_remove", "identity": joinerIdentity})
	removed := creator.await("text_removed", commandTimeout)
	if got := evInt(t, removed, "epoch"); got <= 1 {
		t.Fatalf("text removal did not advance epoch (got %d, want > 1)", got)
	}
}
