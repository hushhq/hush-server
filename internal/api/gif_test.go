package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tenorOkBody = `{
  "results": [
    {
      "id": "tenor-1",
      "media_formats": {
        "gif":     { "url": "https://media.tenor.test/x.gif",      "dims": [320, 240] },
        "tinygif": { "url": "https://media.tenor.test/x-small.gif", "dims": [120,  90] }
      }
    },
    {
      "id": "tenor-2",
      "media_formats": {
        "gif":     { "url": "https://media.tenor.test/y.gif",      "dims": [640, 480] }
      }
    }
  ]
}`

func mountGifTestServer(handler http.Handler) (*httptest.Server, func()) {
	ts := httptest.NewServer(handler)
	old := tenorEndpoint
	tenorEndpoint = ts.URL
	return ts, func() {
		tenorEndpoint = old
		ts.Close()
	}
}

func TestGifSearch_HappyPath_RewritesUpstreamShape(t *testing.T) {
	var hits atomic.Int32
	ts, cleanup := mountGifTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		assert.Equal(t, "test-key", r.URL.Query().Get("key"))
		assert.Equal(t, "hush", r.URL.Query().Get("client_key"))
		assert.Equal(t, "cat", r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tenorOkBody))
	}))
	defer cleanup()
	_ = ts

	r := GifRoutes("test-key")
	req := httptest.NewRequest(http.MethodGet, "/search?q=cat&limit=20", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp gifSearchResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Results, 2)
	assert.Equal(t, "tenor-1", resp.Results[0].ID)
	assert.Equal(t, "https://media.tenor.test/x.gif", resp.Results[0].URL)
	assert.Equal(t, "https://media.tenor.test/x-small.gif", resp.Results[0].PreviewURL)
	assert.Equal(t, 320, resp.Results[0].Width)
	assert.Equal(t, 240, resp.Results[0].Height)
	// Second result has no tinygif → preview falls back to gif url.
	assert.Equal(t, "https://media.tenor.test/y.gif", resp.Results[1].PreviewURL)
}

func TestGifSearch_CachesSecondCall(t *testing.T) {
	var hits atomic.Int32
	ts, cleanup := mountGifTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tenorOkBody))
	}))
	defer cleanup()
	_ = ts

	r := GifRoutes("test-key")
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/search?q=cat&limit=20", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}
	assert.Equal(t, int32(1), hits.Load(), "second call should hit cache")
}

func TestGifSearch_NoApiKey_Returns503(t *testing.T) {
	r := GifRoutes("")
	req := httptest.NewRequest(http.MethodGet, "/search?q=cat", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestGifSearch_RejectsOversizeQuery(t *testing.T) {
	r := GifRoutes("test-key")
	long := strings.Repeat("a", gifQueryLimit+1)
	req := httptest.NewRequest(http.MethodGet, "/search?q="+long, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGifSearch_MissingQuery_Returns400(t *testing.T) {
	r := GifRoutes("test-key")
	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
