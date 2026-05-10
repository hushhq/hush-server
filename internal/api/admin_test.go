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
	"github.com/hushhq/hush-server/internal/livekit"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAdminBootstrapSecret = "bootstrap-secret"
const testAdminCookieValue = "admin-cookie-token"

func adminRouter(store *mockStore) http.Handler {
	return adminRouterWithRoomService(store, livekit.NoopRoomService{})
}

func adminRouterWithRoomService(store *mockStore, rs livekit.RoomService) http.Handler {
	return AdminAPIRoutes(
		store,
		testAdminBootstrapSecret,
		24*time.Hour,
		false,
		"",
		nil,
		nil,
		rs,
		nil,
		nil,
		time.Time{},
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
	maxRegisteredUsers := 500
	req, store := authenticatedAdminRequest(http.MethodGet, "/config", nil, "owner")
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:                       "cfg-1",
			Name:                     "Hush Instance",
			RegistrationMode:         "invite_only",
			GuildDiscovery:           "allowed",
			ServerCreationPolicy:     "open",
			MaxRegisteredUsers:       &maxRegisteredUsers,
			ScreenShareResolutionCap: "720p",
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var response adminConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&response))
	assert.Equal(t, "invite_only", response.RegistrationMode)
	assert.Equal(t, "allowed", response.GuildDiscovery)
	require.NotNil(t, response.MaxRegisteredUsers)
	assert.Equal(t, 500, *response.MaxRegisteredUsers)
	assert.Equal(t, "720p", response.ScreenShareResolutionCap)
}

func TestAdminUpdateConfig_RejectsCrossOriginWrites(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{}, "owner")
	req.Header.Set("Origin", "https://malicious.example")
	router := adminRouter(store)

	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminUpdateConfig_GuildDiscoveryReturns204(t *testing.T) {
	maxRegisteredUsers := 250
	screenShareResolutionCap := "720p"
	req, store := authenticatedAdminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{
		GuildDiscovery:           func() *string { value := "required"; return &value }(),
		MaxRegisteredUsers:       &maxRegisteredUsers,
		ScreenShareResolutionCap: &screenShareResolutionCap,
	}, "owner")
	var updatedGuildDiscovery *string
	var updatedMaxRegisteredUsers *int
	var updatedScreenShareResolutionCap *string
	store.updateInstanceConfigFn = func(_ context.Context, _, _, _, guildDiscovery, _ *string, _ *int, _ *int, maxUsers *int, resolutionCap *string) error {
		updatedGuildDiscovery = guildDiscovery
		updatedMaxRegisteredUsers = maxUsers
		updatedScreenShareResolutionCap = resolutionCap
		return nil
	}
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:                       "cfg-1",
			Name:                     "Hush",
			RegistrationMode:         "open",
			GuildDiscovery:           "required",
			ServerCreationPolicy:     "open",
			MaxRegisteredUsers:       &maxRegisteredUsers,
			ScreenShareResolutionCap: "720p",
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
	require.NotNil(t, updatedMaxRegisteredUsers)
	assert.Equal(t, 250, *updatedMaxRegisteredUsers)
	require.NotNil(t, updatedScreenShareResolutionCap)
	assert.Equal(t, "720p", *updatedScreenShareResolutionCap)
}

func TestAdminUpdateConfig_RejectsInvalidScreenShareResolutionCap(t *testing.T) {
	value := "4k"
	req, store := authenticatedAdminRequest(http.MethodPut, "/config", adminUpdateConfigRequest{
		ScreenShareResolutionCap: &value,
	}, "owner")
	router := adminRouter(store)

	rr := doAdmin(router, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
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

func TestAdminChangePassword_RequiresSession(t *testing.T) {
	router := adminRouter(&mockStore{})

	req := adminRequest(http.MethodPost, "/session/change-password", adminChangePasswordRequest{
		CurrentPassword: "current-password-abc",
		NewPassword:     "new-password-xyz-123",
	})
	req.Header.Set("Origin", requestOrigin(req))
	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminChangePassword_MissingFields_Returns400(t *testing.T) {
	req, store := authenticatedAdminRequest(http.MethodPost, "/session/change-password", map[string]string{
		"currentPassword": "",
		"newPassword":     "",
	}, "admin")
	router := adminRouter(store)

	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAdminChangePassword_WrongCurrentPassword_Returns401(t *testing.T) {
	correctHash, err := auth.HashAdminPassword("correct-password-123")
	require.NoError(t, err)

	req, store := authenticatedAdminRequest(http.MethodPost, "/session/change-password", adminChangePasswordRequest{
		CurrentPassword: "wrong-password-xyz",
		NewPassword:     "brand-new-password-abc",
	}, "admin")
	store.getInstanceAdminByIDFn = func(_ context.Context, id string) (*models.InstanceAdmin, error) {
		return &models.InstanceAdmin{
			ID:           id,
			Username:     "testadmin",
			PasswordHash: correctHash,
			Role:         "admin",
			IsActive:     true,
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAdminChangePassword_SameAsCurrent_Returns400(t *testing.T) {
	const password = "same-password-12345"
	correctHash, err := auth.HashAdminPassword(password)
	require.NoError(t, err)

	req, store := authenticatedAdminRequest(http.MethodPost, "/session/change-password", adminChangePasswordRequest{
		CurrentPassword: password,
		NewPassword:     password,
	}, "admin")
	store.getInstanceAdminByIDFn = func(_ context.Context, id string) (*models.InstanceAdmin, error) {
		return &models.InstanceAdmin{
			ID:           id,
			Username:     "testadmin",
			PasswordHash: correctHash,
			Role:         "admin",
			IsActive:     true,
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Contains(t, body["error"], "differ")
}

func TestAdminChangePassword_WeakNewPassword_Returns400(t *testing.T) {
	correctHash, err := auth.HashAdminPassword("correct-password-123")
	require.NoError(t, err)

	req, store := authenticatedAdminRequest(http.MethodPost, "/session/change-password", adminChangePasswordRequest{
		CurrentPassword: "correct-password-123",
		NewPassword:     "short",
	}, "admin")
	store.getInstanceAdminByIDFn = func(_ context.Context, id string) (*models.InstanceAdmin, error) {
		return &models.InstanceAdmin{
			ID:           id,
			Username:     "testadmin",
			PasswordHash: correctHash,
			Role:         "admin",
			IsActive:     true,
		}, nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAdminChangePassword_Success_Returns204(t *testing.T) {
	const currentPassword = "current-password-abc"
	const newPassword = "new-password-xyz-9999"

	correctHash, err := auth.HashAdminPassword(currentPassword)
	require.NoError(t, err)

	var capturedHash string
	req, store := authenticatedAdminRequest(http.MethodPost, "/session/change-password", adminChangePasswordRequest{
		CurrentPassword: currentPassword,
		NewPassword:     newPassword,
	}, "admin")
	store.getInstanceAdminByIDFn = func(_ context.Context, id string) (*models.InstanceAdmin, error) {
		return &models.InstanceAdmin{
			ID:           id,
			Username:     "testadmin",
			PasswordHash: correctHash,
			Role:         "admin",
			IsActive:     true,
		}, nil
	}
	store.updateInstanceAdminPasswordFn = func(_ context.Context, id, passwordHash string) error {
		capturedHash = passwordHash
		return nil
	}
	router := adminRouter(store)

	rr := doAdmin(router, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.NotEmpty(t, capturedHash, "store must have been called with a new hash")

	// Verify the stored hash matches the new password using the canonical function.
	ok, err := auth.VerifyAdminPassword(newPassword, capturedHash)
	require.NoError(t, err)
	assert.True(t, ok, "stored hash must verify against newPassword")

	// Verify the stored hash does NOT match the old password.
	oldOk, err := auth.VerifyAdminPassword(currentPassword, capturedHash)
	require.NoError(t, err)
	assert.False(t, oldOk, "stored hash must not verify against currentPassword")
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

// ---------- Instance ban LiveKit eviction ----------

// adminFakeRoomServiceCall mirrors the moderation_test fakeRoomService
// shape; it is duplicated locally because this test file lives in the
// same package and referencing the moderation_test types from outside
// a *_test.go would not compile in CI builds where the test files are
// compiled per-package.
type adminFakeRoomServiceCall struct {
	room     string
	identity string
}

type adminFakeRoomService struct {
	calls []adminFakeRoomServiceCall
}

func (f *adminFakeRoomService) RemoveParticipant(_ context.Context, room, identity string) error {
	f.calls = append(f.calls, adminFakeRoomServiceCall{room: room, identity: identity})
	return nil
}

// TestInstanceBan_EvictsFromAllVoiceChannels proves an instance-wide
// ban iterates every guild the user belongs to, lists each guild's
// voice channels, and asks LiveKit to evict the user from each one.
// This is the moderation gap closed in slice 17.
func TestInstanceBan_EvictsFromAllVoiceChannels(t *testing.T) {
	targetUserID := uuid.NewString()
	guildA := uuid.NewString()
	guildB := uuid.NewString()
	voiceA1 := uuid.NewString()
	voiceA2 := uuid.NewString()
	voiceB1 := uuid.NewString()
	textA := uuid.NewString()

	req, store := authenticatedAdminRequest(http.MethodPost, "/bans",
		models.InstanceBanRequest{UserID: targetUserID, Reason: "policy"}, "owner")
	store.deleteSessionsByUserIDFn = func(_ context.Context, _ string) error { return nil }
	store.insertInstanceBanByAdminFn = func(_ context.Context, _, _, _ string, _ *time.Time) (*models.InstanceBan, error) {
		return &models.InstanceBan{ID: uuid.NewString()}, nil
	}
	store.listServersForUserFn = func(_ context.Context, _ string) ([]models.Server, error) {
		return []models.Server{{ID: guildA}, {ID: guildB}}, nil
	}
	store.removeServerMemberFn = func(_ context.Context, _, _ string) error { return nil }
	store.listChannelsFn = func(_ context.Context, sid string) ([]models.Channel, error) {
		switch sid {
		case guildA:
			return []models.Channel{
				{ID: voiceA1, Type: "voice"},
				{ID: textA, Type: "text"},
				{ID: voiceA2, Type: "voice"},
			}, nil
		case guildB:
			return []models.Channel{{ID: voiceB1, Type: "voice"}}, nil
		}
		return nil, nil
	}

	rs := &adminFakeRoomService{}
	router := adminRouterWithRoomService(store, rs)
	rr := doAdmin(router, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Len(t, rs.calls, 3, "instance ban must evict every voice channel across every guild")
	rooms := map[string]bool{}
	for _, c := range rs.calls {
		rooms[c.room] = true
		assert.Equal(t, targetUserID, c.identity)
	}
	assert.True(t, rooms["channel-"+voiceA1])
	assert.True(t, rooms["channel-"+voiceA2])
	assert.True(t, rooms["channel-"+voiceB1])
	assert.False(t, rooms["channel-"+textA], "non-voice channels must not be evicted")
}
