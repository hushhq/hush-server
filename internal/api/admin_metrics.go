package api

import (
	"net/http"
	"runtime"
	"time"

	"github.com/hushhq/hush-server/internal/version"
	"github.com/hushhq/hush-server/internal/ws"
)

// HubStatsProvider is satisfied by *ws.Hub. Declared as an interface so the
// metrics handler is testable without spinning up a real WebSocket Hub.
type HubStatsProvider interface {
	Stats() ws.HubStats
}

// adminMetricsResponse is the JSON shape returned by GET /api/admin/metrics.
//
// The endpoint is intentionally narrow: traffic shape, WebSocket population,
// and basic process telemetry. It is not a Prometheus surface and does not
// expose latency histograms, per-route counters, or any payload-derived data.
type adminMetricsResponse struct {
	Timestamp     string              `json:"timestamp"`
	UptimeSeconds float64             `json:"uptimeSeconds"`
	Version       string              `json:"version"`
	HTTP          HTTPMetricsSnapshot `json:"http"`
	WebSocket     ws.HubStats         `json:"websocket"`
	Process       processSnapshot     `json:"process"`
}

type processSnapshot struct {
	Goroutines int    `json:"goroutines"`
	HeapBytes  uint64 `json:"heapBytes"`
	NumCPU     int    `json:"numCpu"`
}

// metrics handles GET /api/admin/metrics.
func (h *adminHandler) metrics(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	resp := adminMetricsResponse{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		UptimeSeconds: time.Since(h.startedAt).Seconds(),
		Version:       version.ServerVersion,
		HTTP:          h.httpMetrics.Snapshot(),
		Process: processSnapshot{
			Goroutines: runtime.NumGoroutine(),
			HeapBytes:  memStats.HeapAlloc,
			NumCPU:     runtime.NumCPU(),
		},
	}
	if h.hubStats != nil {
		resp.WebSocket = h.hubStats.Stats()
	}
	writeJSON(w, http.StatusOK, resp)
}
