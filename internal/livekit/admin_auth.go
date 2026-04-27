package livekit

import (
	"context"
	"net/http"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/twitchtv/twirp"
)

// adminTokenValidFor caps the lifetime of admin JWTs minted for
// outbound RoomService calls. Five minutes is enough to ride out
// transient retries while keeping the blast radius small if a
// token were ever leaked from logs or a heap dump.
const adminTokenValidFor = 5 * time.Minute

// withAdminAuth attaches an Authorization header carrying a short-
// lived RoomAdmin JWT scoped to the target room. The returned
// context drives Twirp's HTTP transport to send the header on the
// outbound RemoveParticipant request.
func withAdminAuth(ctx context.Context, apiKey, apiSecret, room string) (context.Context, error) {
	at := auth.NewAccessToken(apiKey, apiSecret)
	at.SetVideoGrant(&auth.VideoGrant{
		RoomAdmin: true,
		Room:      room,
	}).SetValidFor(adminTokenValidFor)

	jwt, err := at.ToJWT()
	if err != nil {
		return ctx, err
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+jwt)
	return twirp.WithHTTPRequestHeaders(ctx, header)
}
