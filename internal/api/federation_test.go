package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- federated-verify endpoint ----------

func TestFederatedVerify_Returns403(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/federated-verify", map[string]string{
		"publicKey":  "dGVzdA==",
		"signature":  "dGVzdA==",
		"instanceId": "remote.example.com",
	})

	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "federation is not supported in this MVP", resp["error"])
}

// ---------- RequireAuth rejects federated JWT ----------

func TestRequireAuth_FederatedJWT_Returns403(t *testing.T) {
	fedToken, err := auth.SignFederatedJWT("fed-user-123", "session-id", testJWTSecret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, _ string) (*models.Session, error) {
			return nil, nil
		},
	}

	rr := getWithAuth(newTestRouter(store), "/me", fedToken)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "federation is not supported in this MVP", resp["error"])
}
