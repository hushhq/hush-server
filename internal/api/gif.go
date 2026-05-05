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

// tenorEndpoint is the Tenor v2 search URL. Overridden in tests so the
// proxy logic can be exercised without hitting Google.
var tenorEndpoint = "https://tenor.googleapis.com/v2/search"

// gifSearchTTL is the in-memory cache window. Tenor charges per
// request and the same query is hit by every user picker open, so we
// cache aggressively but not so long that trending results stale.
const gifSearchTTL = 60 * time.Second

// gifSearchLimitMax caps how many results a single search returns. The
// picker grid only renders the first ~20, so we hard-cap above that to
// keep response payloads bounded.
const gifSearchLimitMax = 30

// gifQueryLimit caps the query string length to keep the upstream URL
// sane and to bound the cache key size.
const gifQueryLimit = 80

// GifRoutes mounts the GIF proxy at /api/gif. The handler is mounted
// under RequireAuth in main.go so anonymous requests do not waste
// upstream quota.
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

type gifSearchResponse struct {
	Results []gifResult `json:"results"`
}

type gifResult struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	PreviewURL string `json:"previewUrl"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

func (h *gifHandler) search(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "GIF picker is not configured on this instance",
		})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing q"})
		return
	}
	if len(q) > gifQueryLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("q exceeds %d chars", gifQueryLimit),
		})
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > gifSearchLimitMax {
		limit = gifSearchLimitMax
	}

	cacheKey := q + "|" + strconv.Itoa(limit)
	if cached, ok := h.cache.get(cacheKey); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	upstream, err := h.fetchUpstream(r.Context(), q, limit)
	if err != nil {
		slog.Error("gif search upstream", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream gif search failed"})
		return
	}
	h.cache.set(cacheKey, upstream)
	writeJSON(w, http.StatusOK, upstream)
}

func (h *gifHandler) fetchUpstream(ctx context.Context, q string, limit int) (gifSearchResponse, error) {
	params := url.Values{}
	params.Set("key", h.apiKey)
	params.Set("client_key", "hush")
	params.Set("q", q)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("media_filter", "gif,tinygif")
	params.Set("contentfilter", "high")

	reqURL := tenorEndpoint + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return gifSearchResponse{}, err
	}
	res, err := h.client.Do(req)
	if err != nil {
		return gifSearchResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return gifSearchResponse{}, fmt.Errorf("tenor %d: %s", res.StatusCode, string(body))
	}

	var raw struct {
		Results []struct {
			ID           string `json:"id"`
			MediaFormats struct {
				Gif struct {
					URL  string `json:"url"`
					Dims []int  `json:"dims"`
				} `json:"gif"`
				TinyGif struct {
					URL  string `json:"url"`
					Dims []int  `json:"dims"`
				} `json:"tinygif"`
			} `json:"media_formats"`
		} `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return gifSearchResponse{}, fmt.Errorf("tenor decode: %w", err)
	}

	out := gifSearchResponse{Results: make([]gifResult, 0, len(raw.Results))}
	for _, r := range raw.Results {
		gif := r.MediaFormats.Gif
		if gif.URL == "" {
			continue
		}
		w, hgt := 0, 0
		if len(gif.Dims) >= 2 {
			w, hgt = gif.Dims[0], gif.Dims[1]
		}
		preview := r.MediaFormats.TinyGif.URL
		if preview == "" {
			preview = gif.URL
		}
		out.Results = append(out.Results, gifResult{
			ID:         r.ID,
			URL:        gif.URL,
			PreviewURL: preview,
			Width:      w,
			Height:     hgt,
		})
	}
	return out, nil
}

// gifCache is a tiny TTL'd cache keyed by `q|limit`. Concurrency-safe
// for the dozen-or-so concurrent dialog opens we expect; the lock cost
// is negligible compared with the upstream fetch we are skipping.
type gifCache struct {
	mu      sync.Mutex
	entries map[string]gifCacheEntry
}

type gifCacheEntry struct {
	value     gifSearchResponse
	expiresAt time.Time
}

func newGifCache() *gifCache {
	return &gifCache{entries: make(map[string]gifCacheEntry)}
}

func (c *gifCache) get(key string) (gifSearchResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return gifSearchResponse{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return gifSearchResponse{}, false
	}
	return entry.value, true
}

func (c *gifCache) set(key string, value gifSearchResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = gifCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(gifSearchTTL),
	}
}
