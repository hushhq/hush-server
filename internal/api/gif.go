package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// giphyEndpoint is the Giphy v1 API host. Overridden in tests so the
// proxy logic can be exercised without hitting the real API.
var giphyEndpoint = "https://api.giphy.com/v1/gifs"

// gifSearchTTL is the in-memory cache window. Giphy charges per
// request and our beta key is rate-limited (100/hour), so caching
// trending + popular queries aggressively is critical.
const gifSearchTTL = 60 * time.Second

// gifSearchLimitMax caps how many results a single page returns. The
// Grid component's infinite scroll requests pages of ~10–25; we hard-
// cap above that to keep response payloads bounded.
const gifSearchLimitMax = 50

// gifQueryLimit caps the query string length to keep the upstream URL
// sane and to bound the cache key size.
const gifQueryLimit = 80

// GifRoutes mounts the GIF proxy at /api/gif. The handler is mounted
// under RequireAuth in main.go so anonymous requests do not waste
// upstream quota.
//
// `q` empty → trending. `q` present → search. Both pass `offset` for
// infinite scroll. The full Giphy response shape is forwarded to the
// client so `@giphy/react-components` Grid can consume it directly.
func GifRoutes(apiKey string) chi.Router {
	r := chi.NewRouter()
	h := newGifHandler(apiKey, http.DefaultClient)
	r.Get("/search", h.search)
	return r
}

type gifHandler struct {
	apiKey string
	client *http.Client
	cache  *gifCache
}

func newGifHandler(apiKey string, client *http.Client) *gifHandler {
	if client == nil {
		client = http.DefaultClient
	}
	return &gifHandler{apiKey: apiKey, client: client, cache: newGifCache()}
}

func (h *gifHandler) search(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "GIF picker is not configured on this instance",
		})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > gifQueryLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("q exceeds %d chars", gifQueryLimit),
		})
		return
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > gifSearchLimitMax {
		limit = gifSearchLimitMax
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			offset = v
		}
	}

	cacheKey := q + "|" + strconv.Itoa(limit) + "|" + strconv.Itoa(offset)
	if cached, ok := h.cache.get(cacheKey); ok {
		writeRaw(w, http.StatusOK, cached)
		return
	}

	body, err := h.fetchUpstream(r.Context(), q, limit, offset)
	if err != nil {
		slog.Error("gif search upstream", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream gif search failed"})
		return
	}
	h.cache.set(cacheKey, body)
	writeRaw(w, http.StatusOK, body)
}

func (h *gifHandler) fetchUpstream(ctx context.Context, q string, limit, offset int) ([]byte, error) {
	params := url.Values{}
	params.Set("api_key", h.apiKey)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	params.Set("rating", "g")
	params.Set("bundle", "messaging_non_clips")

	var path string
	if q == "" {
		path = "/trending"
	} else {
		path = "/search"
		params.Set("q", q)
	}
	reqURL := giphyEndpoint + path + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		preview := body
		if len(preview) > 1024 {
			preview = preview[:1024]
		}
		return nil, fmt.Errorf("giphy %d: %s", res.StatusCode, string(preview))
	}
	// Validate it parses as JSON before caching, but forward the raw
	// payload so the SDK Grid sees Giphy's exact shape.
	var probe struct {
		Data       json.RawMessage `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("giphy decode: %w", err)
	}
	return body, nil
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// gifCache is a tiny TTL'd cache keyed by `q|limit|offset`. Both
// trending paging and search paging benefit because the Grid re-issues
// page 0 on every popover open.
type gifCache struct {
	mu      sync.Mutex
	entries map[string]gifCacheEntry
}

type gifCacheEntry struct {
	value     []byte
	expiresAt time.Time
}

func newGifCache() *gifCache {
	return &gifCache{entries: make(map[string]gifCacheEntry)}
}

func (c *gifCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.value, true
}

func (c *gifCache) set(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = gifCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(gifSearchTTL),
	}
}
