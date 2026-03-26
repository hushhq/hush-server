package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

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
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "My Hush",
			RegistrationMode: "open",
		}, nil
	}
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) {
		return "admin", nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp instanceConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "My Hush", resp.Name)
	assert.Equal(t, "open", resp.RegistrationMode)
	assert.Equal(t, "admin", resp.MyRole)
}

func TestGetInstanceConfig_MemberRole(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "Fresh Instance",
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
	assert.Equal(t, "member", resp.MyRole)
}

func TestGetInstanceConfig_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	router := instanceRouter(store)
	rr := getServer(router, "/", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- GET /instance/members ----------

func TestListMembers_ReturnsAllUsers(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.listMembersFn = func(_ context.Context) ([]models.Member, error) {
		return []models.Member{
			{ID: "u1", Username: "alice", DisplayName: "Alice", Role: "admin"},
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
	assert.Equal(t, "admin", members[0].Role)
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
			{ID: guild1ID},
			{ID: guild2ID},
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
		Reason: "attempted owner ban",
	}, token)

	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "cannot ban the instance owner")
}

// ---------- Routes moved to /api/admin (AdminAPIRoutes) ----------
// Tests for PUT /api/admin/config, GET /api/admin/audit-log, and server template
// CRUD are in admin_test.go.
