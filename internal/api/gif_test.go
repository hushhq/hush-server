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

const giphyOkBody = `{
  "data": [
    {"id":"abc","images":{"original":{"url":"https://media.giphy.test/abc.gif","width":"480","height":"320"}}}
  ],
  "pagination": {"total_count":1,"count":1,"offset":0},
  "meta": {"status":200,"msg":"OK","response_id":"r1"}
}`

func mountGiphyTestServer(handler http.Handler) (*httptest.Server, func()) {
	ts := httptest.NewServer(handler)
	old := giphyEndpoint
	giphyEndpoint = ts.URL
	return ts, func() {
		giphyEndpoint = old
		ts.Close()
	}
}

func TestGifSearch_HappyPath_ForwardsGiphyShape(t *testing.T) {
	var hits atomic.Int32
	var seenPath string
	ts, cleanup := mountGiphyTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		seenPath = r.URL.Path
		assert.Equal(t, "test-key", r.URL.Query().Get("api_key"))
		assert.Equal(t, "cat", r.URL.Query().Get("q"))
		assert.Equal(t, "g", r.URL.Query().Get("rating"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(giphyOkBody))
	}))
	defer cleanup()
	_ = ts

	r := GifRoutes("test-key")
	req := httptest.NewRequest(http.MethodGet, "/search?q=cat&limit=25", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "/search", seenPath)
	var resp struct {
		Data       []map[string]any `json:"data"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "abc", resp.Data[0]["id"])
}

func TestGifSearch_EmptyQuery_HitsTrending(t *testing.T) {
	var seenPath string
	ts, cleanup := mountGiphyTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		assert.Equal(t, "", r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(giphyOkBody))
	}))
	defer cleanup()
	_ = ts

	r := GifRoutes("test-key")
	req := httptest.NewRequest(http.MethodGet, "/search?limit=25", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "/trending", seenPath)
}

func TestGifSearch_CachesSecondCall(t *testing.T) {
	var hits atomic.Int32
	ts, cleanup := mountGiphyTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(giphyOkBody))
	}))
	defer cleanup()
	_ = ts

	r := GifRoutes("test-key")
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/search?q=cat&limit=25&offset=0", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}
	assert.Equal(t, int32(1), hits.Load(), "second call should hit cache")
}

func TestGifSearch_DifferentOffsetsBypassCache(t *testing.T) {
	var hits atomic.Int32
	ts, cleanup := mountGiphyTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(giphyOkBody))
	}))
	defer cleanup()
	_ = ts

	r := GifRoutes("test-key")
	for _, offset := range []string{"0", "25", "50"} {
		req := httptest.NewRequest(http.MethodGet, "/search?q=cat&limit=25&offset="+offset, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}
	assert.Equal(t, int32(3), hits.Load())
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
