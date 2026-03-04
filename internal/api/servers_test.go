package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serversRouter returns a ServerRoutes handler wired with testJWTSecret.
func serversRouter(store *mockStore) http.Handler {
	return ServerRoutes(store, nil, testJWTSecret)
}

// adminRouter returns an AdminRoutes handler wired with testJWTSecret.
func adminRouter(store *mockStore) http.Handler {
	return AdminRoutes(store, testJWTSecret)
}

// ---------- POST / (createServer) ----------

func TestCreateServer_Success(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:                   "inst-1",
				Name:                 "Test",
				RegistrationMode:     "open",
				ServerCreationPolicy: "any_member",
			}, nil
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			assert.Equal(t, "My Guild", name)
			assert.Equal(t, userID, ownerID)
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, srvID, uid, _ string) error {
			assert.Equal(t, serverID, srvID)
			assert.Equal(t, userID, uid)
			return nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "My Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	var server models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&server))
	assert.Equal(t, serverID, server.ID)
	assert.Equal(t, "My Guild", server.Name)
}

// TestCreateServer_CreatorBecomesOwner verifies AddServerMember is called with "owner" role.
func TestCreateServer_CreatorBecomesOwner(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var capturedRole string
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{ServerCreationPolicy: "any_member"}, nil
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _, role string) error {
			capturedRole = role
			return nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Test Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "owner", capturedRole, "creator must be added as guild owner")
}

func TestCreateServer_AdminOnly_MemberForbidden(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{ServerCreationPolicy: "admin_only"}, nil
		},
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "member", nil // member cannot create when policy is admin_only
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Forbidden Guild"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "restricted")
}

func TestCreateServer_AdminOnly_AdminAllowed(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{ServerCreationPolicy: "admin_only"}, nil
		},
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "admin", nil // instance admin is allowed
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Admin Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)
}

func TestCreateServer_EmptyName_Returns400(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{ServerCreationPolicy: "any_member"}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "   "}, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "name is required")
}

// ---------- GET / (listMyServers) ----------

func TestListMyServers(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		listServersForUserFn: func(_ context.Context, uid string) ([]models.Server, error) {
			assert.Equal(t, userID, uid)
			return []models.Server{
				{ID: "srv-1", Name: "Guild One", OwnerID: userID},
				{ID: "srv-2", Name: "Guild Two", OwnerID: uuid.New().String()},
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var servers []models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&servers))
	require.Len(t, servers, 2)
	assert.Equal(t, "Guild One", servers[0].Name)
}

func TestListMyServers_EmptyList_ReturnsEmptyArray(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		listServersForUserFn: func(_ context.Context, _ string) ([]models.Server, error) {
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var servers []models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&servers))
	assert.Empty(t, servers)
}

// ---------- GET /{serverId} (getServer) ----------

func TestGetServer(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, uid string) (string, error) {
			if uid == userID {
				return "member", nil
			}
			return "", nil
		},
		getServerByIDFn: func(_ context.Context, srvID string) (*models.Server, error) {
			assert.Equal(t, serverID, srvID)
			return &models.Server{ID: serverID, Name: "My Guild", OwnerID: userID}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/"+serverID+"/", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var server models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&server))
	assert.Equal(t, serverID, server.ID)
	assert.Equal(t, "My Guild", server.Name)
}

func TestGetServer_NotGuildMember_Returns403(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, _ string) (string, error) {
			return "", nil // empty role means not a member
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/"+serverID+"/", token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------- DELETE /{serverId} (deleteServer) ----------

func TestDeleteServer_OwnerOnly(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var deleted bool
	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, uid string) (string, error) {
			if uid == userID {
				return "owner", nil
			}
			return "", nil
		},
		deleteServerFn: func(_ context.Context, srvID string) error {
			assert.Equal(t, serverID, srvID)
			deleted = true
			return nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := deleteServer(router, "/"+serverID+"/", token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, deleted, "server must be deleted")
}

func TestDeleteServer_AdminForbidden(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, _ string) (string, error) {
			return "admin", nil // admin cannot delete — only owner can
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := deleteServer(router, "/"+serverID+"/", token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "owner")
}

// ---------- GET /{serverId}/members (listMembers) ----------

func TestListMembers(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, uid string) (string, error) {
			if uid == userID {
				return "member", nil
			}
			return "", nil
		},
		listServerMembersFn: func(_ context.Context, srvID string) ([]models.ServerMemberWithUser, error) {
			assert.Equal(t, serverID, srvID)
			return []models.ServerMemberWithUser{
				{ID: userID, Username: "alice", Role: "member", JoinedAt: time.Now()},
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/"+serverID+"/members", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var members []models.ServerMemberWithUser
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	require.Len(t, members, 1)
	assert.Equal(t, "alice", members[0].Username)
}

// ---------- PUT /{serverId}/members/{userId}/role (changeRole) ----------

func TestChangeRole_AdminCanPromote(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	var updatedRole string
	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			switch userID {
			case actorID:
				return "admin", nil
			case targetID:
				return "member", nil
			}
			return "", nil
		},
		updateServerMemberRoleFn: func(_ context.Context, _, _, role string) error {
			updatedRole = role
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := serversRouter(store)

	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changeRoleRequest{NewRole: "mod"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "mod", updatedRole)
}

func TestChangeRole_MemberCannot(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, _ string) (string, error) {
			return "member", nil // everyone is a member
		},
	}
	token := makeAuth(store, actorID)
	router := serversRouter(store)

	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changeRoleRequest{NewRole: "mod"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestChangeRole_CannotPromoteAboveSelf(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			switch userID {
			case actorID:
				return "admin", nil
			case targetID:
				return "member", nil
			}
			return "", nil
		},
	}
	token := makeAuth(store, actorID)
	router := serversRouter(store)

	// Admin trying to promote to "owner" — "owner" is not a valid newRole.
	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changeRoleRequest{NewRole: "owner"}, token)
	// "owner" is rejected as invalid newRole value.
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- GET /admin/guilds (listGuildBillingStats) ----------

func TestListGuildBillingStats_OwnerOnly(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, uid string) (string, error) {
			if uid == userID {
				return "admin", nil // admin, not owner
			}
			return "member", nil
		},
	}
	token := makeAuth(store, userID)
	router := adminRouter(store)

	rr := getServer(router, "/guilds", token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "instance owner")
}

func TestListGuildBillingStats_Returns5Fields(t *testing.T) {
	userID := uuid.New().String()
	now := time.Now()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "owner", nil
		},
		listGuildBillingStatsFn: func(_ context.Context) ([]models.GuildBillingStats, error) {
			return []models.GuildBillingStats{
				{
					ID:           "guild-1",
					MemberCount:  42,
					StorageBytes: 1024 * 1024,
					OwnerID:      uuid.New().String(),
					CreatedAt:    now,
				},
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := adminRouter(store)

	rr := getServer(router, "/guilds", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var stats []models.GuildBillingStats
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&stats))
	require.Len(t, stats, 1)
	assert.Equal(t, "guild-1", stats[0].ID)
	assert.Equal(t, 42, stats[0].MemberCount)
	assert.Equal(t, int64(1024*1024), stats[0].StorageBytes)
	assert.NotEmpty(t, stats[0].OwnerID)
	assert.False(t, stats[0].CreatedAt.IsZero())
}

func TestListGuildBillingStats_EmptyList_ReturnsEmptyArray(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "owner", nil
		},
		listGuildBillingStatsFn: func(_ context.Context) ([]models.GuildBillingStats, error) {
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	router := adminRouter(store)

	rr := getServer(router, "/guilds", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var stats []models.GuildBillingStats
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&stats))
	assert.Empty(t, stats)
}
