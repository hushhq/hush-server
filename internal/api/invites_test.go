package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publicInvitesRouter returns the public invite router (GET /:code, POST /claim).
func publicInvitesRouter(store *mockStore) http.Handler {
	return PublicInviteRoutes(store, testJWTSecret)
}

// guildInvitesRouter returns the guild-scoped invite router (POST /).
// It wraps GuildInviteRoutes with guild context injection for testing.
func guildInvitesRouter(store *mockStore, guildRole string) http.Handler {
	userID := "test-invite-user-id"
	inner := GuildInviteRoutes(store)
	return withGuildContext(userID, guildRole)(inner)
}

func getInvite(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func postInviteJSON(handler http.Handler, path string, body interface{}, token string) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------- GET /invites/:code (public) ----------

func TestGetInviteInfo_Public_ValidCode_ReturnsGuildName(t *testing.T) {
	serverID := uuid.New().String()
	store := &mockStore{}
	store.getInviteByCodeFn = func(_ context.Context, code string) (*models.InviteCode, error) {
		if code == "VALID" {
			return &models.InviteCode{
				Code:      "VALID",
				ServerID:  &serverID,
				Uses:      0,
				MaxUses:   10,
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil
		}
		return nil, nil
	}
	store.getServerByIDFn = func(_ context.Context, sid string) (*models.Server, error) {
		if sid == serverID {
			return &models.Server{ID: serverID, Name: "My Guild"}, nil
		}
		return nil, nil
	}
	router := publicInvitesRouter(store)
	rr := getInvite(router, "/VALID")
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp inviteInfoResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "VALID", resp.Code)
	assert.Equal(t, "My Guild", resp.GuildName)
	assert.Equal(t, serverID, resp.ServerID)
}

func TestGetInviteByCode_NotFound_Returns404(t *testing.T) {
	store := &mockStore{}
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return nil, nil
	}
	router := publicInvitesRouter(store)
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
			Uses:      0,
			MaxUses:   10,
			ExpiresAt: time.Now().Add(-time.Hour),
		}, nil
	}
	router := publicInvitesRouter(store)
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
			Uses:      10,
			MaxUses:   10,
			ExpiresAt: time.Now().Add(time.Hour),
		}, nil
	}
	router := publicInvitesRouter(store)
	rr := getInvite(router, "/FULL")
	assert.Equal(t, http.StatusNotFound, rr.Code)
	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "expired or no longer valid")
}

// ---------- POST /invites (guild-scoped, role-gated) ----------

func TestCreateInvite_ModCanCreate_Returns201(t *testing.T) {
	store := &mockStore{}
	router := guildInvitesRouter(store, "mod")
	rr := postInviteJSON(router, "/", nil, "")
	assert.Equal(t, http.StatusCreated, rr.Code)
	var inv models.InviteCode
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&inv))
	assert.Len(t, inv.Code, inviteCodeLength)
	assert.Equal(t, defaultInviteMaxUses, inv.MaxUses)
}

func TestCreateInvite_AdminCanCreate_Returns201(t *testing.T) {
	store := &mockStore{}
	router := guildInvitesRouter(store, "admin")
	rr := postInviteJSON(router, "/", nil, "")
	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestCreateInvite_MemberForbidden_Returns403(t *testing.T) {
	store := &mockStore{}
	router := guildInvitesRouter(store, "member")
	rr := postInviteJSON(router, "/", nil, "")
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "mod")
}

func TestCreateInvite_CustomMaxUses(t *testing.T) {
	store := &mockStore{}
	var capturedMaxUses int
	store.createInviteFn = func(_ context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error) {
		capturedMaxUses = maxUses
		return &models.InviteCode{Code: code, CreatedBy: createdBy, MaxUses: maxUses, ExpiresAt: expiresAt}, nil
	}
	router := guildInvitesRouter(store, "admin")
	rr := postInviteJSON(router, "/", models.CreateInviteRequest{MaxUses: ptrInt(10), ExpiresIn: ptrInt(3600)}, "")
	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, 10, capturedMaxUses)
}

func TestCreateInvite_InvalidMaxUses_Returns400(t *testing.T) {
	store := &mockStore{}
	router := guildInvitesRouter(store, "admin")
	rr := postInviteJSON(router, "/", models.CreateInviteRequest{MaxUses: ptrInt(0)}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateInvite_InvalidExpiresIn_Returns400(t *testing.T) {
	store := &mockStore{}
	router := guildInvitesRouter(store, "admin")
	rr := postInviteJSON(router, "/", models.CreateInviteRequest{ExpiresIn: ptrInt(30)}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- POST /invites/claim ----------

func TestClaimInvite_ValidCode_Returns200(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getInviteByCodeFn = func(_ context.Context, code string) (*models.InviteCode, error) {
		if code == "CLAIM1" {
			return &models.InviteCode{
				Code:      "CLAIM1",
				ServerID:  &serverID,
				Uses:      0,
				MaxUses:   10,
				ExpiresAt: time.Now().Add(time.Hour),
			}, nil
		}
		return nil, nil
	}
	store.claimInviteUseFn = func(_ context.Context, code string) (bool, error) {
		assert.Equal(t, "CLAIM1", code)
		return true, nil
	}
	store.getServerByIDFn = func(_ context.Context, _ string) (*models.Server, error) {
		return &models.Server{ID: serverID, Name: "Test Guild"}, nil
	}
	router := publicInvitesRouter(store)
	rr := postInviteJSON(router, "/claim", map[string]string{"code": "CLAIM1"}, token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp claimInviteResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, serverID, resp.ServerID)
	assert.Equal(t, "Test Guild", resp.GuildName)
}

func TestClaimInvite_ExpiredCode_Returns400(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return &models.InviteCode{
			Code:      "OLD",
			ExpiresAt: time.Now().Add(-time.Hour),
		}, nil
	}
	router := publicInvitesRouter(store)
	rr := postInviteJSON(router, "/claim", map[string]string{"code": "OLD"}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestClaimInvite_MaxUsesReached_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getInviteByCodeFn = func(_ context.Context, code string) (*models.InviteCode, error) {
		return &models.InviteCode{Code: code, ServerID: &serverID, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	store.claimInviteUseFn = func(_ context.Context, _ string) (bool, error) {
		return false, nil
	}
	router := publicInvitesRouter(store)
	rr := postInviteJSON(router, "/claim", map[string]string{"code": "FULL"}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestCreateInvite_NoAuth_Returns401 verifies the claim route requires auth.
func TestCreateInvite_NoAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	router := publicInvitesRouter(store)
	rr := postInviteJSON(router, "/claim", map[string]string{"code": "ANYCODE"}, "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
