package livekit

import (
	"fmt"
	"time"

	"github.com/livekit/protocol/auth"
)

// TokenOptions captures everything needed to mint a LiveKit access token.
//
// Metadata is optional. When non-empty it is set on the access token via
// `AccessToken.SetMetadata` and reaches each remote participant as
// `participant.metadata`. The voice-MLS layer relies on that field to
// resolve the device-scoped credential identity (`userId:deviceId`) when
// evicting a departed participant from the MLS group, because
// `participant.identity` itself is intentionally only the application
// user id (used for moderation and UI labels). See
// `internal/api/livekit.go` for how the metadata JSON is constructed and
// `hush-web/src/lib/voiceParticipantMetadata.js` for the consuming
// parser.
type TokenOptions struct {
	APIKey          string
	APISecret       string
	Identity        string
	RoomName        string
	ParticipantName string
	// Metadata, if non-empty, is forwarded verbatim as the participant
	// metadata claim on the JWT. Callers are responsible for choosing
	// a stable, parser-friendly encoding (the existing call site uses
	// a small JSON object).
	Metadata string
	ValidFor time.Duration
}

// GenerateAccessToken mints a LiveKit access token from a TokenOptions
// bundle. Returns an error when APIKey or APISecret is empty so a
// misconfigured deployment fails fast rather than silently issuing
// unsigned tokens.
func GenerateAccessToken(opts TokenOptions) (string, error) {
	if opts.APIKey == "" || opts.APISecret == "" {
		return "", fmt.Errorf("LiveKit API key and secret required")
	}
	at := auth.NewAccessToken(opts.APIKey, opts.APISecret)
	grant := &auth.VideoGrant{
		RoomJoin:       true,
		Room:           opts.RoomName,
		CanPublish:     boolPtr(true),
		CanSubscribe:   boolPtr(true),
		CanPublishData: boolPtr(true),
	}
	at.SetVideoGrant(grant).
		SetIdentity(opts.Identity).
		SetName(opts.ParticipantName).
		SetValidFor(opts.ValidFor)
	if opts.Metadata != "" {
		at.SetMetadata(opts.Metadata)
	}
	return at.ToJWT()
}

// GenerateToken is the legacy positional wrapper around
// GenerateAccessToken. New code should prefer GenerateAccessToken so
// metadata (and any future option) can be plumbed without rewriting
// every call site.
func GenerateToken(apiKey, apiSecret, identity, roomName, participantName string, validFor time.Duration) (string, error) {
	return GenerateAccessToken(TokenOptions{
		APIKey:          apiKey,
		APISecret:       apiSecret,
		Identity:        identity,
		RoomName:        roomName,
		ParticipantName: participantName,
		ValidFor:        validFor,
	})
}

func boolPtr(b bool) *bool {
	return &b
}
