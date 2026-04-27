package api

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hijackableRecorder is a minimal http.ResponseWriter that satisfies
// http.Hijacker for the test. The httptest.ResponseRecorder does not
// implement Hijacker, so we use a tiny stand-in to verify the
// statusRecorder forwards Hijack to the wrapped writer.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, errors.New("hijack-ok")
}

// Regression test for the production /ws 500 outage:
// HTTPMetricsMiddleware wraps the ResponseWriter with statusRecorder,
// which must continue to satisfy http.Hijacker so gorilla/websocket
// upgrades work. Without the passthrough the WS handshake returns a
// 500 from `Upgrade`'s "response does not implement http.Hijacker"
// branch, breaking every reconnect.
func TestStatusRecorder_PassesThroughHijack(t *testing.T) {
	metrics := NewHTTPMetrics()
	mw := HTTPMetricsMiddleware(metrics)

	hijackerInvoked := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		require.True(t, ok, "ResponseWriter MUST implement http.Hijacker after the metrics wrapper")
		_, _, err := hj.Hijack()
		require.Error(t, err)
		assert.Equal(t, "hijack-ok", err.Error(), "Hijack must reach the underlying writer")
		hijackerInvoked = true
	}))

	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))

	assert.True(t, hijackerInvoked, "handler must have observed a Hijacker")
	assert.True(t, rec.hijacked, "underlying writer's Hijack must have been called")
}
