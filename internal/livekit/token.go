package livekit

import (
	"fmt"
	"time"

	"github.com/livekit/protocol/auth"
)

// GenerateToken creates a LiveKit access token for the given identity, room, and participant name.
// apiKey and apiSecret are from LiveKit server config.
func GenerateToken(apiKey, apiSecret, identity, roomName, participantName string, validFor time.Duration) (string, error) {
	if apiKey == "" || apiSecret == "" {
		return "", fmt.Errorf("LiveKit API key and secret required")
	}
	at := auth.NewAccessToken(apiKey, apiSecret)
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		Room:      roomName,
		CanPublish:   boolPtr(true),
		CanSubscribe: boolPtr(true),
		CanPublishData: boolPtr(true),
	}
	at.SetVideoGrant(grant).
		SetIdentity(identity).
		SetName(participantName).
		SetValidFor(validFor)
	return at.ToJWT()
}

func boolPtr(b bool) *bool {
	return &b
}
