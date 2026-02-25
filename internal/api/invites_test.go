package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func invitesRouter(store *mockStore) http.Handler {
	return InviteRoutes(store)
}

func getInvite(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestGetInviteByCode_ValidCode_ReturnsServerInfo(t *testing.T) {
	serverID := "s1"
	store := &mockStore{}
	store.getInviteByCodeFn = func(_ context.Context, code string) (*models.InviteCode, error) {
		if code == "VALID" {
			return &models.InviteCode{
				Code:      "VALID",
				ServerID:  serverID,
				Uses:      0,
				MaxUses:   10,
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil
		}
		return nil, nil
	}
	store.getServerByIDFn = func(_ context.Context, sid string) (*models.Server, error) {
		if sid == serverID {
			return &models.Server{ID: serverID, Name: "Test Server"}, nil
		}
		return nil, nil
	}
	router := invitesRouter(store)
	rr := getInvite(router, "/VALID")
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp inviteInfoResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, serverID, resp.ServerID)
	assert.Equal(t, "Test Server", resp.ServerName)
}

func TestGetInviteByCode_NotFound_Returns404(t *testing.T) {
	store := &mockStore{}
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return nil, nil
	}
	router := invitesRouter(store)
	rr := getInvite(router, "/NONEXIST")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "invite not found")
}

func TestGetInviteByCode_Expired_Returns404(t *testing.T) {
	store := &mockStore{}
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return &models.InviteCode{
			Code:      "EXPIRED",
			ServerID:  "s1",
			Uses:      0,
			MaxUses:   10,
			ExpiresAt: time.Now().Add(-time.Hour),
		}, nil
	}
	router := invitesRouter(store)
	rr := getInvite(router, "/EXPIRED")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "expired")
}

func TestGetInviteByCode_MaxUsesReached_Returns404(t *testing.T) {
	store := &mockStore{}
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return &models.InviteCode{
			Code:      "FULL",
			ServerID:  "s1",
			Uses:      10,
			MaxUses:   10,
			ExpiresAt: time.Now().Add(time.Hour),
		}, nil
	}
	router := invitesRouter(store)
	rr := getInvite(router, "/FULL")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "expired or no longer valid")
}
