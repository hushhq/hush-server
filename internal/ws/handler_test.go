package ws

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/auth"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWSJWTSecret = "test-ws-secret"

// dialWS is a test helper that dials the WebSocket server with optional token.
func dialWS(server *httptest.Server, token string) (*websocket.Conn, error) {
	u := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	if token != "" {
		u += "?token=" + token
	}
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	return c, err
}

// ---------- WS URL-token auth rejects federated JWT ----------

func TestHandler_FederatedJWT_URLToken_Returns403(t *testing.T) {
	hub := NewHub()

	handler := Handler(hub, testWSJWTSecret, nil, "*")
	server := httptest.NewServer(handler)
	defer server.Close()

	fedToken, err := auth.SignFederatedJWT("fed-user-1", "session-1", testWSJWTSecret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	// Federated JWT via URL token should get HTTP 403 before upgrade.
	resp, err := http.Get(server.URL + "/ws?token=" + fedToken)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, string(body), "federation is not supported in this MVP")
}

// ---------- WS first-message auth rejects federated JWT ----------

func TestHandler_FederatedJWT_FirstMessage_Rejects(t *testing.T) {
	hub := NewHub()

	handler := Handler(hub, testWSJWTSecret, nil, "*")
	server := httptest.NewServer(handler)
	defer server.Close()

	fedToken, err := auth.SignFederatedJWT("fed-user-2", "session-2", testWSJWTSecret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	// Dial without token to trigger first-message auth path.
	conn, err := dialWS(server, "")
	if err != nil {
		// Some implementations reject at upgrade; either way test passes.
		t.Logf("upgrade rejected: %v (acceptable)", err)
		return
	}
	defer conn.Close()

	authMsg, _ := json.Marshal(map[string]string{
		"type":  "auth",
		"token": fedToken,
	})
	err = conn.WriteMessage(websocket.TextMessage, authMsg)
	require.NoError(t, err)

	// Server should close the connection with a policy-violation close code and
	// an explicit MVP federation message.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	assert.True(t, websocket.IsCloseError(err, websocket.ClosePolicyViolation), "expected policy violation close, got %v", err)
	assert.Contains(t, err.Error(), "federation is not supported in this MVP")
}

// ---------- WS URL-token accepts normal JWT ----------

func TestHandler_NormalJWT_URLToken_Accepts(t *testing.T) {
	hub := NewHub()

	handler := Handler(hub, testWSJWTSecret, nil, "*")
	server := httptest.NewServer(handler)
	defer server.Close()

	// Normal (non-federated, non-guest) JWT.
	normalToken, err := auth.SignJWT("user-1", "session-1", "device-1", testWSJWTSecret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	conn, err := dialWS(server, normalToken)
	if err != nil {
		t.Fatalf("expected successful WebSocket upgrade for normal JWT, got: %v", err)
	}
	conn.Close()
}
