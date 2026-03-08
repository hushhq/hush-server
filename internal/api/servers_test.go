package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
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

// TestChangeRole_EmitsSystemMessage verifies changeRole calls EmitSystemMessage
// with event_type="role_changed" and metadata containing old_role/new_role.
func TestChangeRole_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	var sysMsgCalled bool
	var capturedEventType string
	var capturedMetadata map[string]interface{}
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
		updateServerMemberRoleFn: func(_ context.Context, _, _, _ string) error {
			return nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, metadata map[string]interface{}) (*models.SystemMessage, error) {
			sysMsgCalled = true
			capturedEventType = eventType
			capturedMetadata = metadata
			return &models.SystemMessage{ID: uuid.New().String()}, nil
		},
	}
	token := makeAuth(store, actorID)
	router := ServerRoutes(store, &mockHub{}, testJWTSecret)

	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changeRoleRequest{NewRole: "mod"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "changeRole must emit system message")
	assert.Equal(t, "role_changed", capturedEventType)
	require.NotNil(t, capturedMetadata)
	assert.Equal(t, "member", capturedMetadata["old_role"])
	assert.Equal(t, "mod", capturedMetadata["new_role"])
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

// ---------- createServer template tests ----------

// channelCreation records a CreateChannel call for test assertions.
type channelCreation struct {
	ServerID  string
	Name      string
	Type      string
	VoiceMode *string
	ParentID  *string
	Position  int
}

// TestCreateServer_Template verifies createServer creates 3 template channels from instance_config,
// broadcasts channel_created for each, and emits a server_created system message.
func TestCreateServer_Template(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	quality := "quality"

	var mu sync.Mutex
	var createdChannels []channelCreation
	var systemMessages []string
	channelCounter := 0

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ServerCreationPolicy: "any_member",
			}, nil
		},
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return &models.ServerTemplate{
				ID:        uuid.New().String(),
				Name:      "Default",
				IsDefault: true,
				Channels: []models.TemplateChannel{
					{Name: "system", Type: "system", Position: -1},
					{Name: "general", Type: "text", Position: 0},
					{Name: "General", Type: "voice", VoiceMode: &quality, Position: 1},
				},
			}, nil
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _, _ string) error { return nil },
		getChannelByNameAndTypeFn: func(_ context.Context, _, _, _ string) (*models.Channel, error) {
			return nil, nil // no existing channels
		},
		createChannelFn: func(_ context.Context, srvID, name, chType string, voiceMode *string, parentID *string, position int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			channelCounter++
			createdChannels = append(createdChannels, channelCreation{
				ServerID: srvID, Name: name, Type: chType,
				VoiceMode: voiceMode, ParentID: parentID, Position: position,
			})
			sid := srvID
			return &models.Channel{
				ID: fmt.Sprintf("ch-%d", channelCounter), ServerID: &sid,
				Name: name, Type: chType, VoiceMode: voiceMode, Position: position,
			}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			systemMessages = append(systemMessages, eventType)
			return &models.SystemMessage{ID: uuid.New().String(), EventType: eventType}, nil
		},
	}

	hub := &mockHub{}
	token := makeAuth(store, userID)
	router := serversRouterWithHub(store, hub)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Template Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	// Should create 3 channels
	require.Len(t, createdChannels, 3, "expected 3 template channels to be created")
	assert.Equal(t, "system", createdChannels[0].Name)
	assert.Equal(t, "system", createdChannels[0].Type)
	assert.Equal(t, -1, createdChannels[0].Position)
	assert.Equal(t, "general", createdChannels[1].Name)
	assert.Equal(t, "text", createdChannels[1].Type)
	assert.Equal(t, 0, createdChannels[1].Position)
	assert.Equal(t, "General", createdChannels[2].Name)
	assert.Equal(t, "voice", createdChannels[2].Type)
	require.NotNil(t, createdChannels[2].VoiceMode)
	assert.Equal(t, "quality", *createdChannels[2].VoiceMode)
	assert.Equal(t, 1, createdChannels[2].Position)

	// 3 channel_created broadcasts + 1 system_message broadcast (for server_created)
	require.GreaterOrEqual(t, len(hub.broadcastCalls), 3, "expected at least 3 broadcasts for channel_created")

	// Verify channel_created broadcasts
	for i := 0; i < 3; i++ {
		var msg map[string]interface{}
		require.NoError(t, json.Unmarshal(hub.broadcastCalls[i].message, &msg))
		assert.Equal(t, "channel_created", msg["type"])
		assert.Equal(t, serverID, hub.broadcastCalls[i].serverID)
	}

	// system_message: server_created should be emitted
	assert.Contains(t, systemMessages, "server_created")
}

// TestCreateServer_PartialFail verifies that when one CreateChannel call fails,
// server creation still succeeds (201) and template_partial_failure system message is emitted.
func TestCreateServer_PartialFail(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var mu sync.Mutex
	var systemMessages []string
	callCount := 0

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ServerCreationPolicy: "any_member",
			}, nil
		},
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return &models.ServerTemplate{
				ID: uuid.New().String(), Name: "Default", IsDefault: true,
				Channels: []models.TemplateChannel{
					{Name: "system", Type: "system", Position: -1},
					{Name: "general", Type: "text", Position: 0},
				},
			}, nil
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _, _ string) error { return nil },
		getChannelByNameAndTypeFn: func(_ context.Context, _, _, _ string) (*models.Channel, error) {
			return nil, nil
		},
		createChannelFn: func(_ context.Context, _, name, chType string, _ *string, _ *string, _ int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if callCount == 2 { // fail on second channel
				return nil, errors.New("db error")
			}
			sid := serverID
			return &models.Channel{
				ID: fmt.Sprintf("ch-%d", callCount), ServerID: &sid,
				Name: name, Type: chType,
			}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			systemMessages = append(systemMessages, eventType)
			return &models.SystemMessage{ID: uuid.New().String(), EventType: eventType}, nil
		},
	}

	hub := &mockHub{}
	token := makeAuth(store, userID)
	router := serversRouterWithHub(store, hub)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Partial Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code, "server creation should still succeed on partial template failure")

	// Both server_created and template_partial_failure should be emitted.
	assert.Contains(t, systemMessages, "server_created")
	assert.Contains(t, systemMessages, "template_partial_failure")
}

// TestCreateServer_Idempotent verifies that when GetChannelByNameAndType returns an existing channel,
// that template entry is skipped (no duplicate CreateChannel call).
func TestCreateServer_Idempotent(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var mu sync.Mutex
	createCount := 0

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ServerCreationPolicy: "any_member",
			}, nil
		},
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return &models.ServerTemplate{
				ID: uuid.New().String(), Name: "Default", IsDefault: true,
				Channels: []models.TemplateChannel{
					{Name: "system", Type: "system", Position: -1},
					{Name: "general", Type: "text", Position: 0},
				},
			}, nil
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _, _ string) error { return nil },
		getChannelByNameAndTypeFn: func(_ context.Context, _, name, chType string) (*models.Channel, error) {
			// system channel already exists
			if name == "system" && chType == "system" {
				sid := serverID
				return &models.Channel{ID: "existing-sys", ServerID: &sid, Name: "system", Type: "system"}, nil
			}
			return nil, nil
		},
		createChannelFn: func(_ context.Context, _, name, chType string, _ *string, _ *string, _ int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			createCount++
			sid := serverID
			return &models.Channel{
				ID: fmt.Sprintf("ch-%d", createCount), ServerID: &sid,
				Name: name, Type: chType,
			}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			return &models.SystemMessage{ID: uuid.New().String(), EventType: eventType}, nil
		},
	}

	hub := &mockHub{}
	token := makeAuth(store, userID)
	router := serversRouterWithHub(store, hub)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Idempotent Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	// Only 1 channel should be created (general), system was skipped due to idempotency.
	assert.Equal(t, 1, createCount, "system channel should have been skipped; only general should be created")
}

// TestCreateServer_NoTemplate verifies that when no default template exists, hardcoded 3-channel template is used.
func TestCreateServer_NoTemplate(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var mu sync.Mutex
	var createdNames []string

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ServerCreationPolicy: "any_member",
			}, nil
		},
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return nil, nil // no default template
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _, _ string) error { return nil },
		getChannelByNameAndTypeFn: func(_ context.Context, _, _, _ string) (*models.Channel, error) {
			return nil, nil
		},
		createChannelFn: func(_ context.Context, _, name, _ string, _ *string, _ *string, _ int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			createdNames = append(createdNames, name)
			sid := serverID
			return &models.Channel{ID: uuid.New().String(), ServerID: &sid, Name: name}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			return &models.SystemMessage{ID: uuid.New().String(), EventType: eventType}, nil
		},
	}

	hub := &mockHub{}
	token := makeAuth(store, userID)
	router := serversRouterWithHub(store, hub)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Default Template Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	require.Len(t, createdNames, 3, "hardcoded default template should create 3 channels")
	assert.Equal(t, "system", createdNames[0])
	assert.Equal(t, "general", createdNames[1])
	assert.Equal(t, "General", createdNames[2])
}

// TestCreateServer_SystemAlwaysIncluded verifies that when template has no system entry,
// system channel is prepended automatically.
func TestCreateServer_SystemAlwaysIncluded(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var mu sync.Mutex
	var createdNames []string

	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ServerCreationPolicy: "any_member",
			}, nil
		},
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return &models.ServerTemplate{
				ID: uuid.New().String(), Name: "Default", IsDefault: true,
				Channels: []models.TemplateChannel{
					// Template without system channel
					{Name: "general", Type: "text", Position: 0},
				},
			}, nil
		},
		createServerFn: func(_ context.Context, name, ownerID string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: name, OwnerID: ownerID}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _, _ string) error { return nil },
		getChannelByNameAndTypeFn: func(_ context.Context, _, _, _ string) (*models.Channel, error) {
			return nil, nil
		},
		createChannelFn: func(_ context.Context, _, name, chType string, _ *string, _ *string, _ int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			createdNames = append(createdNames, name)
			sid := serverID
			return &models.Channel{ID: uuid.New().String(), ServerID: &sid, Name: name, Type: chType}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			return &models.SystemMessage{ID: uuid.New().String(), EventType: eventType}, nil
		},
	}

	hub := &mockHub{}
	token := makeAuth(store, userID)
	router := serversRouterWithHub(store, hub)

	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "Auto System Guild"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	require.Len(t, createdNames, 2, "system channel should have been auto-prepended")
	assert.Equal(t, "system", createdNames[0], "system channel must be created first")
	assert.Equal(t, "general", createdNames[1])
}
