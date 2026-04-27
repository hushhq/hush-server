// Package livekit provides server-side helpers for working with a
// LiveKit deployment: signing participant tokens (token.go) and
// invoking the LiveKit RoomService for moderation actions
// (this file).
//
// The RoomService is exposed as an interface so callers (moderation
// and admin-ban paths) can be exercised against an in-memory fake
// in unit tests, while production wires the Twirp client backed by
// the real LiveKit server.

package livekit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	lkproto "github.com/livekit/protocol/livekit"
)

// roomServiceTimeout caps every outbound request to the LiveKit
// RoomService. It is intentionally short: this call is in the hot
// path of a moderation action, and we accept eventual consistency
// (the ban succeeds even if eviction is briefly unreachable).
const roomServiceTimeout = 5 * time.Second

// RoomService is the moderation-facing surface of LiveKit's
// RoomService API. Only the methods actually invoked by Hush
// moderation flows are exposed.
type RoomService interface {
	// RemoveParticipant terminates the participant's connection to
	// the named room on the LiveKit server. Returns nil if the
	// participant was removed or was already absent. Returns a
	// non-nil error if the LiveKit server could not be reached or
	// returned an error other than "participant not found".
	RemoveParticipant(ctx context.Context, room, identity string) error
}

// NoopRoomService is the safe fallback used when no LiveKit URL is
// configured. It records nothing and always returns nil so callers
// can wire it unconditionally.
type NoopRoomService struct{}

// RemoveParticipant satisfies RoomService for the no-op variant.
func (NoopRoomService) RemoveParticipant(_ context.Context, _, _ string) error {
	return nil
}

// TwirpRoomService is the production RoomService backed by the
// LiveKit Twirp HTTP API. It is safe for concurrent use.
type TwirpRoomService struct {
	client    lkproto.RoomService
	apiKey    string
	apiSecret string
}

// NewTwirpRoomService constructs a RoomService bound to a LiveKit
// deployment.
//
// liveKitURL accepts the same value used for participant signaling
// (e.g. "wss://livekit.example.com" or "https://livekit.example.com");
// the scheme is normalized to https/http for the Twirp endpoint.
//
// Returns NoopRoomService when liveKitURL, apiKey, or apiSecret is
// empty so deployments without a configured LiveKit (development,
// CI) keep working without nil-checks at every call site.
func NewTwirpRoomService(liveKitURL, apiKey, apiSecret string) RoomService {
	if liveKitURL == "" || apiKey == "" || apiSecret == "" {
		return NoopRoomService{}
	}
	httpURL := normalizeRoomServiceURL(liveKitURL)
	httpClient := &http.Client{Timeout: roomServiceTimeout}
	client := lkproto.NewRoomServiceProtobufClient(httpURL, httpClient)
	return &TwirpRoomService{
		client:    client,
		apiKey:    apiKey,
		apiSecret: apiSecret,
	}
}

// RemoveParticipant signs a short-lived RoomAdmin token for the
// target room and invokes RoomService/RemoveParticipant. A
// "participant not found" response from LiveKit is treated as a
// successful no-op because the participant may have already left
// between the moderation decision and the eviction call.
func (s *TwirpRoomService) RemoveParticipant(ctx context.Context, room, identity string) error {
	if room == "" || identity == "" {
		return errors.New("livekit: room and identity are required")
	}
	authedCtx, err := withAdminAuth(ctx, s.apiKey, s.apiSecret, room)
	if err != nil {
		return fmt.Errorf("livekit: build admin auth: %w", err)
	}
	if _, callErr := s.client.RemoveParticipant(authedCtx, &lkproto.RoomParticipantIdentity{
		Room:     room,
		Identity: identity,
	}); callErr != nil {
		if isParticipantNotFound(callErr) {
			return nil
		}
		return fmt.Errorf("livekit: remove participant: %w", callErr)
	}
	return nil
}

// normalizeRoomServiceURL converts a participant signaling URL
// (ws://, wss://) to the Twirp HTTP endpoint accepted by the
// LiveKit RoomService. http/https URLs are returned unchanged.
func normalizeRoomServiceURL(url string) string {
	switch {
	case strings.HasPrefix(url, "wss://"):
		return "https://" + strings.TrimPrefix(url, "wss://")
	case strings.HasPrefix(url, "ws://"):
		return "http://" + strings.TrimPrefix(url, "ws://")
	default:
		return url
	}
}

// isParticipantNotFound returns true when the LiveKit error
// indicates the target participant is not present in the room. We
// classify this as a successful eviction because the moderation
// goal (target is no longer in the room) is already satisfied.
func isParticipantNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "participant not found") ||
		strings.Contains(msg, "not_found") ||
		strings.Contains(msg, "404")
}
