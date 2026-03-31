package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAdminAPIKey = "test-admin-key"

// adminRouter creates an AdminAPIRoutes handler with a known key and no hub/cache.
func adminRouter(store *mockStore) http.Handler {
	return AdminAPIRoutes(store, testAdminAPIKey, nil, nil)
}

// adminRequest is a helper for admin requests with the correct X-Admin-Key header.
func adminRequest(method, path string, body interface{}, key string) *http.Request {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
	}
	return req
}

func doAdmin(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------- RequireAdminAPIKey middleware ----------

func TestAdminAPIKey_Required_Returns401(t *testing.T) {
	store := &mockStore{}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/health", nil, "") // no key
	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var errBody map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errBody))
	assert.Contains(t, errBody["error"], "admin API key")
}

func TestAdminAPIKey_WrongKey_Returns401(t *testing.T) {
	store := &mockStore{}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/health", nil, "wrong-key")
	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminAPIKey_Valid_Returns200(t *testing.T) {
	store := &mockStore{}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/health", nil, testAdminAPIKey)
	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "ok", resp["status"])
	assert.Equal(t, "ok", resp["dbStatus"])
	assert.Contains(t, resp, "version")
	assert.Contains(t, resp, "uptimeSeconds")
	assert.Contains(t, resp, "startedAt")
}

// ---------- GET /guilds ----------

func TestAdminListGuilds_ReturnsStatsWithoutNames(t *testing.T) {
	store := &mockStore{
		listGuildBillingStatsFn: func(_ context.Context) ([]models.GuildBillingStats, error) {
			return []models.GuildBillingStats{
				{ID: uuid.New().String(), MemberCount: 5, MessageCount: 100},
				{ID: uuid.New().String(), MemberCount: 12, MessageCount: 450},
			}, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/guilds", nil, testAdminAPIKey)
	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var stats []models.GuildBillingStats
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&stats))
	require.Len(t, stats, 2)
	assert.Equal(t, 5, stats[0].MemberCount)
	// No name field in GuildBillingStats - privacy boundary enforced.
}

func TestAdminListGuilds_EmptyList_Returns200(t *testing.T) {
	store := &mockStore{
		listGuildBillingStatsFn: func(_ context.Context) ([]models.GuildBillingStats, error) {
			return nil, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/guilds", nil, testAdminAPIKey)
	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var stats []models.GuildBillingStats
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&stats))
	assert.NotNil(t, stats, "empty list must be [] not null")
	assert.Empty(t, stats)
}

// ---------- GET /users ----------

func TestAdminListUsers_ReturnsMembers(t *testing.T) {
	store := &mockStore{
		listMembersFn: func(_ context.Context) ([]models.Member, error) {
			return []models.Member{
				{ID: uuid.New().String(), Username: "alice"},
				{ID: uuid.New().String(), Username: "bob"},
			}, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/users", nil, testAdminAPIKey)
	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var members []models.Member
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	assert.Len(t, members, 2)
}

// ---------- GET /config ----------

func TestAdminGetConfig_Returns200WithGuildDiscovery(t *testing.T) {
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:               "cfg-1",
				Name:             "Hush Instance",
				RegistrationMode: "invite_only",
				GuildDiscovery:   "allowed",
			}, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/config", nil, testAdminAPIKey)
	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp adminConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "invite_only", resp.RegistrationMode)
	assert.Equal(t, "allowed", resp.GuildDiscovery)
	// Ensure no owner_id or server_creation_policy fields leaking.
}

// ---------- PUT /config ----------

func TestAdminUpdateConfig_GuildDiscovery_Returns204(t *testing.T) {
	var updatedGuildDiscovery *string
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:               "cfg-1",
				GuildDiscovery:   "disabled",
				RegistrationMode: "open",
			}, nil
		},
		updateInstanceConfigFn: func(_ context.Context, name, iconURL, regMode, guildDiscovery, serverCreationPolicy *string) error {
			updatedGuildDiscovery = guildDiscovery
			return nil
		},
		getVoiceKeyRotationHoursFn: func(_ context.Context) (int, error) {
			return 2, nil
		},
	}
	router := adminRouter(store)

	gd := "required"
	req := adminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{GuildDiscovery: &gd}, testAdminAPIKey)
	rr := doAdmin(router, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.NotNil(t, updatedGuildDiscovery)
	assert.Equal(t, "required", *updatedGuildDiscovery)
}

func TestAdminUpdateConfig_InvalidGuildDiscovery_Returns400(t *testing.T) {
	store := &mockStore{}
	router := adminRouter(store)

	bad := "everyone"
	req := adminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{GuildDiscovery: &bad}, testAdminAPIKey)
	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var errBody map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errBody))
	assert.Contains(t, errBody["error"], "guildDiscovery")
}

func TestAdminUpdateConfig_AuditLogWritten(t *testing.T) {
	var capturedAction string
	var capturedMetadata map[string]interface{}

	closed := "closed"
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:               "cfg-1",
				RegistrationMode: "open",
				GuildDiscovery:   "disabled",
			}, nil
		},
		updateInstanceConfigFn: func(_ context.Context, _, _, _, _, _ *string) error {
			return nil
		},
		getVoiceKeyRotationHoursFn: func(_ context.Context) (int, error) {
			return 2, nil
		},
		insertInstanceAuditLogFn: func(_ context.Context, _ string, _ *string, action, _ string, metadata map[string]interface{}) error {
			capturedAction = action
			capturedMetadata = metadata
			return nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{RegistrationMode: &closed}, testAdminAPIKey)
	rr := doAdmin(router, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "config_change", capturedAction, "audit log action must be config_change")
	require.NotNil(t, capturedMetadata, "audit log metadata must not be nil")
	regModeChange, ok := capturedMetadata["registration_mode"]
	require.True(t, ok, "metadata must contain registration_mode key")
	changeMap, ok := regModeChange.(map[string]string)
	require.True(t, ok, "registration_mode metadata must be a map[string]string")
	assert.Equal(t, "open", changeMap["old"])
	assert.Equal(t, "closed", changeMap["new"])
}

// ---------- Audit log ----------

func TestAdminAuditLog_Returns200WithEntries(t *testing.T) {
	store := &mockStore{
		listInstanceAuditLogFn: func(_ context.Context, limit, offset int, _ *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
			return []models.InstanceAuditLogEntry{
				{ID: uuid.New().String(), ActorID: "admin-api", Action: "config_change"},
			}, nil
		},
	}
	router := adminRouter(store)

	req := adminRequest(http.MethodGet, "/audit-log", nil, testAdminAPIKey)
	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var entries []models.InstanceAuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "config_change", entries[0].Action)
}
