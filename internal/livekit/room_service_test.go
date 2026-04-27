package livekit

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeRoomServiceURL_RewritesWebSocketScheme verifies the Twirp
// endpoint derived from a participant signaling URL uses the right HTTP
// scheme. LiveKit deployments typically expose both the WebSocket
// signaling and the Twirp HTTP RoomService on the same host.
func TestNormalizeRoomServiceURL_RewritesWebSocketScheme(t *testing.T) {
	cases := map[string]string{
		"wss://example.com":      "https://example.com",
		"ws://example.com:7880":  "http://example.com:7880",
		"https://example.com":    "https://example.com",
		"http://example.com":     "http://example.com",
		"":                       "",
	}
	for input, expected := range cases {
		assert.Equal(t, expected, normalizeRoomServiceURL(input), "input=%q", input)
	}
}

// TestIsParticipantNotFound_RecognizesNotFoundShapes verifies that the
// error classifier accepts the various ways LiveKit reports an absent
// participant. Misclassifying a real failure as "not found" would mask
// a moderation gap, so the keyword set is conservative.
func TestIsParticipantNotFound_RecognizesNotFoundShapes(t *testing.T) {
	require.True(t, isParticipantNotFound(errors.New("twirp: not_found: participant not found")))
	require.True(t, isParticipantNotFound(errors.New("404 Not Found")))
	require.True(t, isParticipantNotFound(errors.New("rpc error: not_found")))

	require.False(t, isParticipantNotFound(nil))
	require.False(t, isParticipantNotFound(errors.New("connection refused")))
	require.False(t, isParticipantNotFound(errors.New("unauthorized")))
}

// TestNoopRoomService_IsAlwaysSuccessful ensures the fallback never
// fails so wiring code can call it unconditionally.
func TestNoopRoomService_IsAlwaysSuccessful(t *testing.T) {
	var rs RoomService = NoopRoomService{}
	require.NoError(t, rs.RemoveParticipant(context.Background(), "channel-x", "user-y"))
}

// TestNewTwirpRoomService_FallsBackToNoopWhenUnconfigured verifies that
// a deployment without a LiveKit URL or credentials does not get a
// half-configured Twirp client. This avoids surprising NPEs at the
// first ban after deploy.
func TestNewTwirpRoomService_FallsBackToNoopWhenUnconfigured(t *testing.T) {
	cases := []struct {
		url, key, secret string
	}{
		{"", "k", "s"},
		{"wss://x", "", "s"},
		{"wss://x", "k", ""},
	}
	for _, c := range cases {
		got := NewTwirpRoomService(c.url, c.key, c.secret)
		_, isNoop := got.(NoopRoomService)
		assert.True(t, isNoop, "url=%q key=%q secret=%q", c.url, c.key, c.secret)
	}

	// Fully configured: returns the real Twirp impl, not the noop.
	configured := NewTwirpRoomService("wss://livekit.example.com", "k", "s")
	_, isNoop := configured.(NoopRoomService)
	assert.False(t, isNoop, "fully configured constructor must return TwirpRoomService")
}

// TestWithAdminAuth_AttachesBearerHeader proves the admin-token
// signer produces a context that the Twirp transport will use for
// the outbound HTTP request.
func TestWithAdminAuth_AttachesBearerHeader(t *testing.T) {
	ctx, err := withAdminAuth(context.Background(), "k", "s", "channel-1")
	require.NoError(t, err)

	// The header is read back through the Twirp ctx accessor in the
	// real call path; here we verify the value via the public helper
	// from the twirp package.
	header, ok := twirpRequestHeaders(ctx)
	require.True(t, ok)
	auth := header.Get("Authorization")
	require.NotEmpty(t, auth)
	require.True(t, len(auth) > len("Bearer "))
	require.Equal(t, "Bearer ", auth[:len("Bearer ")])
}
