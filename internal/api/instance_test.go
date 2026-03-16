package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func instanceRouter(store *mockStore) http.Handler {
	return InstanceRoutes(store, nil, testJWTSecret, NewInstanceCache())
}

// ---------- GET /instance ----------

func TestGetInstanceConfig_ReturnsConfig(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	ownerID := uuid.New().String()
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "My Hush",
			OwnerID:          &ownerID,
			RegistrationMode: "open",
		}, nil
	}
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) {
		return "owner", nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp instanceConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "My Hush", resp.Name)
	assert.Equal(t, "open", resp.RegistrationMode)
	assert.True(t, resp.Bootstrapped, "bootstrapped must be true when ownerID is set")
	assert.Equal(t, "owner", resp.MyRole)
}

func TestGetInstanceConfig_Unbootstrapped_BootstrappedFalse(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "Fresh Instance",
			OwnerID:          nil,
			RegistrationMode: "open",
		}, nil
	}
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) {
		return "member", nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp instanceConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Bootstrapped, "bootstrapped must be false when ownerID is nil")
	assert.Equal(t, "member", resp.MyRole)
}

func TestGetInstanceConfig_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	router := instanceRouter(store)
	rr := getServer(router, "/", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- PUT /instance ----------

func TestUpdateInstanceConfig_OwnerCanUpdate_Returns204(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, uid string) (string, error) {
		if uid == userID {
			return "owner", nil
		}
		return "member", nil
	}
	var updatedName string
	store.updateInstanceConfigFn = func(_ context.Context, name *string, _ *string, _ *string, _ *string) error {
		if name != nil {
			updatedName = *name
		}
		return nil
	}
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "Updated Name"}, token)
	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "Updated Name", updatedName)
}

func TestUpdateInstanceConfig_NonOwnerForbidden_Returns403(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "member", nil }
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "Hack"}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "owner")
}

func TestUpdateInstanceConfig_AdminForbidden_Returns403(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "Hack"}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestUpdateInstanceConfig_InvalidRegistrationMode_Returns400(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"registrationMode": "banana"}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- GET /instance/members ----------

func TestListMembers_ReturnsAllUsers(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.listMembersFn = func(_ context.Context) ([]models.Member, error) {
		return []models.Member{
			{ID: "u1", Username: "alice", DisplayName: "Alice", Role: "owner"},
			{ID: "u2", Username: "bob", DisplayName: "Bob", Role: "member"},
		}, nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/members", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var members []models.Member
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	require.Len(t, members, 2)
	assert.Equal(t, "alice", members[0].Username)
	assert.Equal(t, "owner", members[0].Role)
	assert.Equal(t, "bob", members[1].Username)
}

func TestListMembers_EmptyList_ReturnsEmptyArray(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.listMembersFn = func(_ context.Context) ([]models.Member, error) {
		return nil, nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/members", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var members []models.Member
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	assert.Empty(t, members)
}

func TestListMembers_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	router := instanceRouter(store)
	rr := getServer(router, "/members", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- POST /instance/server-templates ----------

func TestCreateServerTemplate_Success(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }
	store.createServerTemplateFn = func(_ context.Context, name string, channels json.RawMessage, isDefault bool) (*models.ServerTemplate, error) {
		return &models.ServerTemplate{ID: uuid.New().String(), Name: name, IsDefault: isDefault}, nil
	}

	quality := "quality"
	body := serverTemplateRequest{
		Name: "Gaming",
		Channels: []models.TemplateChannel{
			{Name: "system", Type: "system", Position: -1},
			{Name: "general", Type: "text", Position: 0},
			{Name: "Lounge", Type: "voice", VoiceMode: &quality, Position: 1},
		},
		IsDefault: false,
	}
	router := instanceRouter(store)
	rr := postServerJSON(router, "/server-templates", body, token)
	require.Equal(t, http.StatusCreated, rr.Code)
}

func TestCreateServerTemplate_SystemRequired(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }

	body := serverTemplateRequest{
		Name: "Bad Template",
		Channels: []models.TemplateChannel{
			{Name: "general", Type: "text", Position: 0},
		},
	}
	router := instanceRouter(store)
	rr := postServerJSON(router, "/server-templates", body, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "system channel is required")
}

func TestCreateServerTemplate_Forbidden(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }

	body := serverTemplateRequest{
		Name: "Test",
		Channels: []models.TemplateChannel{
			{Name: "system", Type: "system", Position: -1},
		},
	}
	router := instanceRouter(store)
	rr := postServerJSON(router, "/server-templates", body, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestCreateServerTemplate_InvalidType(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }

	body := serverTemplateRequest{
		Name: "Bad",
		Channels: []models.TemplateChannel{
			{Name: "system", Type: "system", Position: -1},
			{Name: "weird", Type: "banana", Position: 0},
		},
	}
	router := instanceRouter(store)
	rr := postServerJSON(router, "/server-templates", body, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "invalid channel type")
}

func TestCreateServerTemplate_VoiceRequiresMode(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }

	body := serverTemplateRequest{
		Name: "Bad",
		Channels: []models.TemplateChannel{
			{Name: "system", Type: "system", Position: -1},
			{Name: "voice-ch", Type: "voice", Position: 0},
		},
	}
	router := instanceRouter(store)
	rr := postServerJSON(router, "/server-templates", body, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "voiceMode")
}

func TestCreateServerTemplate_CategoryCannotHaveParentRef(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }

	body := serverTemplateRequest{
		Name: "Bad",
		Channels: []models.TemplateChannel{
			{Name: "system", Type: "system", Position: -1},
			{Name: "Category", Type: "category", ParentRef: ptrString("other"), Position: 0},
		},
	}
	router := instanceRouter(store)
	rr := postServerJSON(router, "/server-templates", body, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "categories cannot have parentRef")
}

// ---------- POST /instance/bans ----------

func TestInstanceBan_Success_CascadesGuilds(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	guild1ID := uuid.New().String()
	guild2ID := uuid.New().String()

	store := &mockStore{}
	token := makeAuth(store, actorID)

	// actor is admin, target is member
	store.getUserRoleFn = func(_ context.Context, uid string) (string, error) {
		if uid == actorID {
			return "admin", nil
		}
		return "member", nil
	}

	var deleteSessionsCalled int32
	store.deleteSessionsByUserIDFn = func(_ context.Context, uid string) error {
		if uid == targetID {
			atomic.AddInt32(&deleteSessionsCalled, 1)
		}
		return nil
	}

	var insertBanCalled int32
	store.insertInstanceBanFn = func(_ context.Context, uid, actor, reason string, _ *time.Time) (*models.InstanceBan, error) {
		assert.Equal(t, targetID, uid)
		assert.Equal(t, actorID, actor)
		atomic.AddInt32(&insertBanCalled, 1)
		return &models.InstanceBan{ID: uuid.New().String(), UserID: uid, ActorID: actor, Reason: reason}, nil
	}

	store.listServersForUserFn = func(_ context.Context, uid string) ([]models.Server, error) {
		assert.Equal(t, targetID, uid)
		return []models.Server{
			{ID: guild1ID, Name: "G1"},
			{ID: guild2ID, Name: "G2"},
		}, nil
	}

	var removeCalls []string
	store.removeServerMemberFn = func(_ context.Context, serverID, uid string) error {
		assert.Equal(t, targetID, uid)
		removeCalls = append(removeCalls, serverID)
		return nil
	}

	var auditLogCalled int32
	store.insertInstanceAuditLogFn = func(_ context.Context, actor string, tid *string, action, _ string, _ map[string]interface{}) error {
		assert.Equal(t, actorID, actor)
		require.NotNil(t, tid)
		assert.Equal(t, targetID, *tid)
		assert.Equal(t, "instance_ban", action)
		atomic.AddInt32(&auditLogCalled, 1)
		return nil
	}

	router := instanceRouter(store)
	rr := postServerJSON(router, "/bans", models.InstanceBanRequest{
		UserID: targetID,
		Reason: "spam",
	}, token)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.EqualValues(t, 1, atomic.LoadInt32(&deleteSessionsCalled), "DeleteSessionsByUserID must be called")
	assert.EqualValues(t, 1, atomic.LoadInt32(&insertBanCalled), "InsertInstanceBan must be called")
	assert.EqualValues(t, 1, atomic.LoadInt32(&auditLogCalled), "InsertInstanceAuditLog must be called")
	assert.ElementsMatch(t, []string{guild1ID, guild2ID}, removeCalls, "RemoveServerMember must be called for each guild")
}

func TestInstanceBan_SelfBan_Returns400(t *testing.T) {
	actorID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }

	router := instanceRouter(store)
	rr := postServerJSON(router, "/bans", models.InstanceBanRequest{
		UserID: actorID,
		Reason: "self-ban attempt",
	}, token)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "cannot ban yourself")
}

func TestInstanceBan_CannotBanOwner_Returns403(t *testing.T) {
	actorID := uuid.New().String()
	ownerID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, uid string) (string, error) {
		if uid == actorID {
			return "admin", nil
		}
		return "owner", nil
	}

	router := instanceRouter(store)
	rr := postServerJSON(router, "/bans", models.InstanceBanRequest{
		UserID: ownerID,
		Reason: "test",
	}, token)

	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "cannot ban the instance owner")
}

func TestInstanceBan_AdminCannotBanAdmin_Returns403(t *testing.T) {
	actorID := uuid.New().String()
	targetAdminID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) {
		// both actor and target are admin
		return "admin", nil
	}

	router := instanceRouter(store)
	rr := postServerJSON(router, "/bans", models.InstanceBanRequest{
		UserID: targetAdminID,
		Reason: "test",
	}, token)

	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin cannot ban another admin")
}

func TestInstanceBan_AuditLogEntry(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, uid string) (string, error) {
		if uid == actorID {
			return "admin", nil
		}
		return "member", nil
	}
	store.insertInstanceBanFn = func(_ context.Context, uid, actor, reason string, _ *time.Time) (*models.InstanceBan, error) {
		return &models.InstanceBan{ID: uuid.New().String(), UserID: uid, ActorID: actor, Reason: reason}, nil
	}

	var capturedAction string
	store.insertInstanceAuditLogFn = func(_ context.Context, _ string, _ *string, action, _ string, _ map[string]interface{}) error {
		capturedAction = action
		return nil
	}

	router := instanceRouter(store)
	rr := postServerJSON(router, "/bans", models.InstanceBanRequest{
		UserID: targetID,
		Reason: "tos violation",
	}, token)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "instance_ban", capturedAction)
}

// ---------- POST /instance/unban ----------

func TestInstanceUnban_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	banID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }

	store.getActiveInstanceBanFn = func(_ context.Context, uid string) (*models.InstanceBan, error) {
		assert.Equal(t, targetID, uid)
		return &models.InstanceBan{ID: banID, UserID: uid, Reason: "spam"}, nil
	}

	var liftBanCalledWith string
	store.liftInstanceBanFn = func(_ context.Context, bid, liftedBy string) error {
		liftBanCalledWith = bid
		assert.Equal(t, actorID, liftedBy)
		return nil
	}

	var auditAction string
	store.insertInstanceAuditLogFn = func(_ context.Context, _ string, _ *string, action, _ string, _ map[string]interface{}) error {
		auditAction = action
		return nil
	}

	router := instanceRouter(store)
	rr := postServerJSON(router, "/unban", models.InstanceUnbanRequest{
		UserID: targetID,
		Reason: "appeal accepted",
	}, token)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, banID, liftBanCalledWith, "LiftInstanceBan must be called with correct ban ID")
	assert.Equal(t, "instance_unban", auditAction)
}

// ---------- GET /instance/users ----------

func TestSearchUsers_ReturnsBanStatus(t *testing.T) {
	actorID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }

	bannedReason := "tos violation"
	store.searchUsersFn = func(_ context.Context, query string, limit int) ([]models.UserSearchResult, error) {
		assert.Equal(t, "alice", query)
		assert.Equal(t, 25, limit)
		return []models.UserSearchResult{
			{
				ID:        uuid.New().String(),
				Username:  "alice",
				Role:      "member",
				IsBanned:  true,
				BanReason: &bannedReason,
			},
		}, nil
	}

	router := instanceRouter(store)
	rr := getServer(router, "/users?q=alice", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var results []models.UserSearchResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&results))
	require.Len(t, results, 1)
	assert.True(t, results[0].IsBanned)
	require.NotNil(t, results[0].BanReason)
	assert.Equal(t, "tos violation", *results[0].BanReason)
}

// ---------- GET /instance/audit-log ----------

func TestInstanceAuditLog_AdminDenied_Returns403(t *testing.T) {
	actorID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, actorID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }

	router := instanceRouter(store)
	rr := getServer(router, "/audit-log", token)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "owner")
}

func TestInstanceAuditLog_OwnerReturnsEntries(t *testing.T) {
	ownerID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, ownerID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }

	store.listInstanceAuditLogFn = func(_ context.Context, limit, offset int, _ *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
		return []models.InstanceAuditLogEntry{
			{ID: uuid.New().String(), ActorID: ownerID, Action: "instance_ban", Reason: "spam"},
			{ID: uuid.New().String(), ActorID: ownerID, Action: "config_change", Reason: "updated name"},
		}, nil
	}

	router := instanceRouter(store)
	rr := getServer(router, "/audit-log", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var entries []models.InstanceAuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	assert.Len(t, entries, 2)
}

// ---------- PUT /instance (audit log) ----------

func TestUpdateConfig_AuditLogEntry(t *testing.T) {
	ownerID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, ownerID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }

	// Provide old config so the handler can diff
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "Old Name",
			RegistrationMode: "open",
		}, nil
	}

	var capturedAction string
	var capturedMetadata map[string]interface{}
	store.insertInstanceAuditLogFn = func(_ context.Context, actor string, tid *string, action, _ string, metadata map[string]interface{}) error {
		capturedAction = action
		capturedMetadata = metadata
		return nil
	}

	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "New Name"}, token)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "config_change", capturedAction)
	require.NotNil(t, capturedMetadata)
	nameChange, ok := capturedMetadata["name"]
	require.True(t, ok, "metadata must contain 'name' key")
	// The handler stores map[string]string; assert the old/new values regardless of map concrete type.
	switch nameMap := nameChange.(type) {
	case map[string]string:
		assert.Equal(t, "Old Name", nameMap["old"])
		assert.Equal(t, "New Name", nameMap["new"])
	case map[string]interface{}:
		assert.Equal(t, "Old Name", nameMap["old"])
		assert.Equal(t, "New Name", nameMap["new"])
	default:
		t.Fatalf("unexpected type for metadata name change: %T", nameChange)
	}
}
