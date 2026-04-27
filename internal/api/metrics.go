package api

import (
	"net/http"
	"sync/atomic"
)

// HTTPMetrics holds atomic counters for the HTTP layer. It is intentionally
// a coarse-grained, allocation-free counter set: total requests served, plus
// per-status-class counters. Designed to feed an operator-facing metrics
// endpoint, not a full Prometheus surface.
//
// All fields are read via the snapshot accessor; do not read raw atomics.
type HTTPMetrics struct {
	requestsTotal atomic.Uint64
	status1xx     atomic.Uint64
	status2xx     atomic.Uint64
	status3xx     atomic.Uint64
	status4xx     atomic.Uint64
	status5xx     atomic.Uint64
	wsAccepted    atomic.Uint64
	wsRejected    atomic.Uint64
}

// HTTPMetricsSnapshot is a point-in-time copy of HTTPMetrics for serialization.
type HTTPMetricsSnapshot struct {
	RequestsTotal uint64 `json:"requestsTotal"`
	Status1xx     uint64 `json:"status1xx"`
	Status2xx     uint64 `json:"status2xx"`
	Status3xx     uint64 `json:"status3xx"`
	Status4xx     uint64 `json:"status4xx"`
	Status5xx     uint64 `json:"status5xx"`
	WSAccepted    uint64 `json:"wsAccepted"`
	WSRejected    uint64 `json:"wsRejected"`
}

// NewHTTPMetrics returns a zero-initialized HTTPMetrics.
func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{}
}

// Snapshot returns the current counter values atomically.
func (m *HTTPMetrics) Snapshot() HTTPMetricsSnapshot {
	if m == nil {
		return HTTPMetricsSnapshot{}
	}
	return HTTPMetricsSnapshot{
		RequestsTotal: m.requestsTotal.Load(),
		Status1xx:     m.status1xx.Load(),
		Status2xx:     m.status2xx.Load(),
		Status3xx:     m.status3xx.Load(),
		Status4xx:     m.status4xx.Load(),
		Status5xx:     m.status5xx.Load(),
		WSAccepted:    m.wsAccepted.Load(),
		WSRejected:    m.wsRejected.Load(),
	}
}

// IncWSAccepted increments the accepted-WebSocket-handshake counter.
func (m *HTTPMetrics) IncWSAccepted() {
	if m == nil {
		return
	}
	m.wsAccepted.Add(1)
}

// IncWSRejected increments the rejected-WebSocket-handshake counter.
func (m *HTTPMetrics) IncWSRejected() {
	if m == nil {
		return
	}
	m.wsRejected.Add(1)
}

// HTTPMetricsMiddleware records request volume and per-status-class counts.
// It does not record latency histograms; that is intentionally out of scope.
func HTTPMetricsMiddleware(m *HTTPMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if m == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			m.requestsTotal.Add(1)
			switch {
			case rec.status >= 500:
				m.status5xx.Add(1)
			case rec.status >= 400:
				m.status4xx.Add(1)
			case rec.status >= 300:
				m.status3xx.Add(1)
			case rec.status >= 200:
				m.status2xx.Add(1)
			default:
				m.status1xx.Add(1)
			}
		})
	}
}

// statusRecorder captures the response status code for the metrics middleware.
// It does not buffer the body; only the status header is intercepted.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}
