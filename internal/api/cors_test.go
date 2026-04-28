package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/cors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preflight builds an OPTIONS request mirroring the browser's CORS
// preflight: Origin, Access-Control-Request-Method, and an explicit
// Access-Control-Request-Headers list. Returns the recorded response.
func preflight(requestHeader string, requestMethod string) *httptest.ResponseRecorder {
	handler := cors.Handler(CORSOptions())(okHandler)
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/link-archive-manifest/abc", nil)
	req.Header.Set("Origin", "app://localhost")
	req.Header.Set("Access-Control-Request-Method", requestMethod)
	req.Header.Set("Access-Control-Request-Headers", requestHeader)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// allowedHeaderValues returns the comma-separated tokens of
// Access-Control-Allow-Headers, lowercased and trimmed, for membership
// assertions independent of casing/whitespace details.
func allowedHeaderValues(rr *httptest.ResponseRecorder) []string {
	raw := rr.Header().Get("Access-Control-Allow-Headers")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ToLower(strings.TrimSpace(p)))
	}
	return out
}

// TestCORSOptions_AllowsLinkArchiveDownloadToken pins that the
// X-Download-Token header used by the NEW-device download plane
// (fetchManifest, requestDownloadWindow, deleteArchive) survives the
// CORS preflight. Without this, packaged desktop renderers fetching
// cross-origin from app://localhost see a generic "Failed to fetch"
// during LinkDevice import.
func TestCORSOptions_AllowsLinkArchiveDownloadToken(t *testing.T) {
	rr := preflight("X-Download-Token", http.MethodGet)
	require.True(t, rr.Code == http.StatusNoContent || rr.Code == http.StatusOK,
		"preflight must succeed; got %d", rr.Code)
	assert.Contains(t, allowedHeaderValues(rr), "x-download-token")
}

// TestCORSOptions_AllowsLinkArchiveUploadToken pins that the
// X-Upload-Token header used by the OLD-device upload plane (uploadChunk
// PUT, requestUploadWindow POST, confirmChunk POST, finalizeArchive POST,
// deleteArchive DELETE) survives the CORS preflight. Same-origin web
// sessions never preflight; this matters for any cross-origin client.
func TestCORSOptions_AllowsLinkArchiveUploadToken(t *testing.T) {
	rr := preflight("X-Upload-Token", http.MethodPost)
	require.True(t, rr.Code == http.StatusNoContent || rr.Code == http.StatusOK,
		"preflight must succeed; got %d", rr.Code)
	assert.Contains(t, allowedHeaderValues(rr), "x-upload-token")
}

// TestCORSOptions_AllowsLinkArchiveChunkSha256 pins that the
// X-Chunk-Sha256 header used by the in-API chunk PUT (postgres_bytea
// backend) survives the CORS preflight.
func TestCORSOptions_AllowsLinkArchiveChunkSha256(t *testing.T) {
	rr := preflight("X-Chunk-Sha256", http.MethodPut)
	require.True(t, rr.Code == http.StatusNoContent || rr.Code == http.StatusOK,
		"preflight must succeed; got %d", rr.Code)
	assert.Contains(t, allowedHeaderValues(rr), "x-chunk-sha256")
}

// TestCORSOptions_StillAllowsAuthorization is a regression guard: the
// generalised allow list must keep the original Authorization /
// Content-Type / Accept entries intact (every authenticated POST relies
// on Authorization and most write endpoints rely on Content-Type).
func TestCORSOptions_StillAllowsAuthorization(t *testing.T) {
	rr := preflight("Authorization, Content-Type", http.MethodPost)
	require.True(t, rr.Code == http.StatusNoContent || rr.Code == http.StatusOK,
		"preflight must succeed; got %d", rr.Code)
	values := allowedHeaderValues(rr)
	assert.Contains(t, values, "authorization")
	assert.Contains(t, values, "content-type")
}
