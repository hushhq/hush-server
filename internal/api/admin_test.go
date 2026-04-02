package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAdminBootstrapSecret = "bootstrap-secret"
const testAdminCookieValue = "admin-cookie-token"

func adminRouter(store *mockStore) http.Handler {
	return AdminAPIRoutes(
		store,
		testAdminBootstrapSecret,
		24*time.Hour,
		false,
		"",
		nil,
		nil,
	)
}

func adminRequest(method, path string, body interface{}) *http.Request {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func authenticatedAdminRequest(method, path string, body interface{}, role string) (*http.Request, *mockStore) {
	adminID := uuid.NewString()
	store := &mockStore{
		getInstanceAdminSessionByTokenHashFn: func(_ context.Context, tokenHash string) (*models.InstanceAdminSession, error) {
			if tokenHash != auth.TokenHash(testAdminCookieValue) {
				return nil, nil
			}
			return &models.InstanceAdminSession{
				ID:        uuid.NewString(),
				AdminID:   adminID,
				TokenHash: tokenHash,
				ExpiresAt: time.Now().UTC().Add(time.Hour),
				CreatedAt: time.Now().UTC(),
			}, nil
		},
		getInstanceAdminByIDFn: func(_ context.Context, id string) (*models.InstanceAdmin, error) {
			if id != adminID {
				return nil, nil
			}
			return &models.InstanceAdmin{
				ID:        adminID,
				Username:  "owner",
				Role:      role,
				IsActive:  true,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}, nil
		},
	}
	req := adminRequest(method, path, body)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: testAdminCookieValue})
	req.Header.Set("Origin", requestOrigin(req))
	return req, store
}

func doAdmin(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestAdminBootstrapStatus_ReturnsAvailableWhenNoAdminsExist(t *testing.T) {
	store := &mockStore{
		countInstanceAdminsFn: func(_ context.Context) (int, error) {
			return 0, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodPost, "/bootstrap/status", nil)
	rr := doAdmin(router, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var response adminBootstrapStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	assert.True(t, response.BootstrapAvailable)
	assert.False(t, response.HasAdmins)
	assert.True(t, response.HasBootstrapSecret)
}

func TestAdminBootstrapClaim_CreatesOwnerAndSessionCookie(t *testing.T) {
	var createdRole string
	store := &mockStore{
		countInstanceAdminsFn: func(_ context.Context) (int, error) {
			return 0, nil
		},
		createInstanceAdminFn: func(_ context.Context, username string, email *string, passwordHash, role string) (*models.InstanceAdmin, error) {
			createdRole = role
			assert.Equal(t, "owner", role)
			assert.Equal(t, "rootadmin", username)
			assert.NotEmpty(t, passwordHash)
			return &models.InstanceAdmin{
				ID:           uuid.NewString(),
				Username:     username,
				Email:        email,
				PasswordHash: passwordHash,
				Role:         role,
				IsActive:     true,
				CreatedAt:    time.Now().UTC(),
				UpdatedAt:    time.Now().UTC(),
			}, nil
		},
		createInstanceAdminSessionFn: func(_ context.Context, sessionID, adminID, tokenHash string, expiresAt time.Time, createdIP, userAgent *string) (*models.InstanceAdminSession, error) {
			assert.NotEmpty(t, sessionID)
			assert.NotEmpty(t, adminID)
			assert.NotEmpty(t, tokenHash)
			return &models.InstanceAdminSession{ID: sessionID, AdminID: adminID, TokenHash: tokenHash, ExpiresAt: expiresAt}, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodPost, "/bootstrap/claim", adminBootstrapClaimRequest{
		BootstrapSecret: testAdminBootstrapSecret,
		Username:        "rootadmin",
		Password:        "super-secure-password",
	})
	rr := doAdmin(router, req)

	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "owner", createdRole)
	cookies := rr.Result().Cookies()
	require.NotEmpty(t, cookies)
	assert.Equal(t, adminSessionCookieName, cookies[0].Name)
}

func TestAdminHealth_RequiresSession(t *testing.T) {
	router := adminRouter(&mockStore{})

	req := adminRequest(http.MethodGet, "/health", nil)
	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminHealth_WithSession_Returns200(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/health", nil, "owner")
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	assert.Equal(t, "ok", response["status"])
}

func TestAdminListGuilds_ReturnsStatsWithoutNames(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/guilds", nil, "owner")
	store.listGuildBillingStatsFn = func(_ context.Context) ([]models.GuildBillingStats, error) {
		return []models.GuildBillingStats{
			{ID: uuid.NewString(), MemberCount: 5, MessageCount: 100},
			{ID: uuid.NewString(), MemberCount: 12, MessageCount: 450},
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var stats []models.GuildBillingStats
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&stats))
	require.Len(t, stats, 2)
	assert.Equal(t, 5, stats[0].MemberCount)
}

func TestAdminGetConfig_Returns200WithGuildDiscovery(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/config", nil, "owner")
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:                   "cfg-1",
			Name:                 "Hush Instance",
			RegistrationMode:     "invite_only",
			GuildDiscovery:       "allowed",
			ServerCreationPolicy: "open",
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var response adminConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	assert.Equal(t, "invite_only", response.RegistrationMode)
	assert.Equal(t, "allowed", response.GuildDiscovery)
}

func TestAdminUpdateConfig_RejectsCrossOriginWrites(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{}, "owner")
	req.Header.Set("Origin", "https://malicious.example")
	router := adminRouter(store)

	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminUpdateConfig_GuildDiscoveryReturns204(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{
		GuildDiscovery: func() *string { value := "required"; return &value }(),
	}, "owner")
	var updatedGuildDiscovery *string
	store.updateInstanceConfigFn = func(_ context.Context, _, _, _, guildDiscovery, _ *string) error {
		updatedGuildDiscovery = guildDiscovery
		return nil
	}
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:                   "cfg-1",
			Name:                 "Hush",
			RegistrationMode:     "open",
			GuildDiscovery:       "required",
			ServerCreationPolicy: "open",
		}, nil
	}
	store.getVoiceKeyRotationHoursFn = func(_ context.Context) (int, error) {
		return 2, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.NotNil(t, updatedGuildDiscovery)
	assert.Equal(t, "required", *updatedGuildDiscovery)
}

func TestAdminListAdmins_RequiresOwnerRole(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/admins", nil, "admin")
	router := adminRouter(store)

	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminListAdmins_OwnerReturnsAdmins(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/admins", nil, "owner")
	store.listInstanceAdminsFn = func(_ context.Context) ([]models.InstanceAdmin, error) {
		return []models.InstanceAdmin{
			{ID: uuid.NewString(), Username: "owner", Role: "owner", IsActive: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			{ID: uuid.NewString(), Username: "ops", Role: "admin", IsActive: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var admins []models.InstanceAdmin
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&admins))
	require.Len(t, admins, 2)
	assert.Equal(t, "owner", admins[0].Role)
}

func TestAdminAuditLog_ReturnsEntries(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodGet, "/audit-log", nil, "owner")
	store.listInstanceAuditLogFn = func(_ context.Context, limit, offset int, _ *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
		assert.Equal(t, 50, limit)
		assert.Equal(t, 0, offset)
		return []models.InstanceAuditLogEntry{
			{ID: uuid.NewString(), ActorID: "user-1", Action: "config_change"},
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var entries []models.InstanceAuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "config_change", entries[0].Action)
}
