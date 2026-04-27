package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/livekit"
	"github.com/hushhq/hush-server/internal/ws"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHubStats struct {
	stats ws.HubStats
}

func (f *fakeHubStats) Stats() ws.HubStats { return f.stats }

func adminRouterWithMetrics(store *mockStore, metrics *HTTPMetrics, hub HubStatsProvider, started time.Time) http.Handler {
	return AdminAPIRoutes(
		store,
		testAdminBootstrapSecret,
		24*time.Hour,
		false,
		"",
		nil,
		nil,
		livekit.NoopRoomService{},
		metrics,
		hub,
		started,
	)
}

func TestAdminMetrics_RequiresSession(t *testing.T) {
	router := adminRouterWithMetrics(&mockStore{}, NewHTTPMetrics(), nil, time.Now())

	req := adminRequest(http.MethodGet, "/metrics", nil)
	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminMetrics_WithSession_ReturnsCounters(t *testing.T) {
	metrics := NewHTTPMetrics()
	hub := &fakeHubStats{stats: ws.HubStats{Clients: 3, PresentIdentities: 2, SubscribedChannels: 4, SubscribedServers: 1}}
	started := time.Now().Add(-2 * time.Hour)

	req, store := authenticatedAdminRequest(http.MethodGet, "/metrics", nil, "owner")
	router := adminRouterWithMetrics(store, metrics, hub, started)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp adminMetricsResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 3, resp.WebSocket.Clients)
	assert.Equal(t, 2, resp.WebSocket.PresentIdentities)
	assert.Equal(t, 4, resp.WebSocket.SubscribedChannels)
	assert.Equal(t, 1, resp.WebSocket.SubscribedServers)
	assert.GreaterOrEqual(t, resp.UptimeSeconds, 7100.0)
	assert.NotEmpty(t, resp.Timestamp)
	assert.GreaterOrEqual(t, resp.Process.NumCPU, 1)
}

func TestHTTPMetricsMiddleware_CountsByStatusClass(t *testing.T) {
	metrics := NewHTTPMetrics()
	mw := HTTPMetricsMiddleware(metrics)

	cases := []struct {
		status   int
		expected func(s HTTPMetricsSnapshot) uint64
	}{
		{http.StatusOK, func(s HTTPMetricsSnapshot) uint64 { return s.Status2xx }},
		{http.StatusFound, func(s HTTPMetricsSnapshot) uint64 { return s.Status3xx }},
		{http.StatusBadRequest, func(s HTTPMetricsSnapshot) uint64 { return s.Status4xx }},
		{http.StatusInternalServerError, func(s HTTPMetricsSnapshot) uint64 { return s.Status5xx }},
	}
	for _, tc := range cases {
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	}

	snap := metrics.Snapshot()
	assert.Equal(t, uint64(4), snap.RequestsTotal)
	assert.Equal(t, uint64(1), snap.Status2xx)
	assert.Equal(t, uint64(1), snap.Status3xx)
	assert.Equal(t, uint64(1), snap.Status4xx)
	assert.Equal(t, uint64(1), snap.Status5xx)
}

func TestHTTPMetrics_NilSafeSnapshot(t *testing.T) {
	var m *HTTPMetrics
	snap := m.Snapshot()
	assert.Equal(t, uint64(0), snap.RequestsTotal)
}

func TestAdminMetrics_NilHubAndMetricsAreSafe(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/metrics", nil, "owner")
	router := adminRouterWithMetrics(store, nil, nil, time.Time{})

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp adminMetricsResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 0, resp.WebSocket.Clients)
	assert.Equal(t, uint64(0), resp.HTTP.RequestsTotal)
}

func TestHubStatsProvider_HubSatisfiesInterface(t *testing.T) {
	var _ HubStatsProvider = ws.NewHub() // compile-time check
	_ = context.Background()
}
