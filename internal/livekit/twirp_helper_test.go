package livekit

import (
	"context"
	"net/http"

	"github.com/twitchtv/twirp"
)

// twirpRequestHeaders is a thin test helper around twirp.HTTPRequestHeaders
// so the room_service_test file does not need to import twirp directly.
func twirpRequestHeaders(ctx context.Context) (http.Header, bool) {
	return twirp.HTTPRequestHeaders(ctx)
}
