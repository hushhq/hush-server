package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serversRouter returns a ServerRoutes handler wired with testJWTSecret.
func serversRouter(store *mockStore) http.Handler {
	return ServerRoutes(store, nil, testJWTSecret)
}

// serversRouterWithHub wires ServerRoutes with a custom hub for broadcast tests.
func serversRouterWithHub(store *mockStore, hub GlobalBroadcaster) http.Handler {
	return ServerRoutes(store, hub, testJWTSecret)
}

// ---------- POST / (createServer) ----------

func TestCreateServer_Success(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	encMeta := []byte(`{"name":"My Guild"}`)

	store := &mockStore{
		createServerFn: func(_ context.Context, metadata []byte) (*models.Server, error) {
			assert.Equal(t, encMeta, metadata)
			return &models.Server{ID: serverID, EncryptedMetadata: metadata}, nil
		},
		addServerMemberFn: func(_ context.Context, srvID, uid string, level int) error {
			assert.Equal(t, serverID, srvID)
			assert.Equal(t, userID, uid)
			assert.Equal(t, models.PermissionLevelOwner, level)
			return nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: encMeta}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	var server models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&server))
	assert.Equal(t, serverID, server.ID)
}

// TestCreateServer_CreatorBecomesOwner verifies AddServerMember is called with PermissionLevelOwner.
func TestCreateServer_CreatorBecomesOwner(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var capturedLevel int
	store := &mockStore{
		createServerFn: func(_ context.Context, metadata []byte) (*models.Server, error) {
			return &models.Server{ID: serverID, EncryptedMetadata: metadata}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _ string, level int) error {
			capturedLevel = level
			return nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, token)
	require.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, models.PermissionLevelOwner, capturedLevel, "creator must be added with owner permission level")
}

func TestCreateServer_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	router := serversRouter(store)
	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, "")
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

// TestCreateServer_PolicyDisabled verifies that createServer returns 403
// when the instance server_creation_policy is "disabled".
func TestCreateServer_PolicyDisabled(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:                   "inst-1",
				Name:                 "Test Instance",
				RegistrationMode:     "open",
				ServerCreationPolicy: "disabled",
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Contains(t, body["error"], "disabled")
}

// TestCreateServer_PolicyPaid verifies that createServer returns 403
// with a subscription message when server_creation_policy is "paid".
func TestCreateServer_PolicyPaid(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:                   "inst-1",
				Name:                 "Test Instance",
				RegistrationMode:     "open",
				ServerCreationPolicy: "paid",
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Contains(t, body["error"], "subscription")
}

// ---------- GET / (listMyServers) ----------

func TestListMyServers(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		listServersForUserFn: func(_ context.Context, uid string) ([]models.Server, error) {
			assert.Equal(t, userID, uid)
			return []models.Server{
				{ID: "srv-1", EncryptedMetadata: []byte(`{"name":"Guild One"}`)},
				{ID: "srv-2", EncryptedMetadata: []byte(`{"name":"Guild Two"}`)},
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
	assert.Equal(t, "srv-1", servers[0].ID)
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
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		getServerByIDFn: func(_ context.Context, srvID string) (*models.Server, error) {
			assert.Equal(t, serverID, srvID)
			return &models.Server{ID: serverID, EncryptedMetadata: []byte(`{"name":"My Guild"}`)}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/"+serverID+"/", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var server models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&server))
	assert.Equal(t, serverID, server.ID)
}

func TestGetServer_NotGuildMember_Returns403(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return 0, errors.New("not a member") // error = not in guild
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
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelOwner, nil
			}
			return 0, errors.New("not a member")
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
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelAdmin, nil // admin cannot delete - only owner can
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
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		listServerMembersFn: func(_ context.Context, srvID string) ([]models.ServerMemberWithUser, error) {
			assert.Equal(t, serverID, srvID)
			return []models.ServerMemberWithUser{
				{ID: userID, Username: "alice", PermissionLevel: models.PermissionLevelMember, JoinedAt: time.Now()},
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

	var updatedLevel int
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			switch userID {
			case actorID:
				return models.PermissionLevelAdmin, nil
			case targetID:
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		updateServerMemberLevelFn: func(_ context.Context, _, _ string, level int) error {
			updatedLevel = level
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := serversRouter(store)

	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changePermissionLevelRequest{PermissionLevel: models.PermissionLevelMod}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, models.PermissionLevelMod, updatedLevel)
}

func TestChangeRole_MemberCannot(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelMember, nil // everyone is member
		},
	}
	token := makeAuth(store, actorID)
	router := serversRouter(store)

	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changePermissionLevelRequest{PermissionLevel: models.PermissionLevelMod}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestChangeRole_CannotPromoteAboveSelf(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			switch userID {
			case actorID:
				return models.PermissionLevelAdmin, nil
			case targetID:
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
	}
	token := makeAuth(store, actorID)
	router := serversRouter(store)

	// Admin (level 2) trying to promote to owner (level 3) - exceeds own level.
	rr := putServerJSON(router, "/"+serverID+"/members/"+targetID+"/role",
		changePermissionLevelRequest{PermissionLevel: models.PermissionLevelOwner}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// TestChangeRole_EmitsSystemMessage verifies changeRole calls EmitSystemMessage
// with event_type="role_changed" and metadata containing old_level/new_level.
func TestChangeRole_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	serverID := uuid.New().String()

	var sysMsgCalled bool
	var capturedEventType string
	var capturedMetadata map[string]interface{}
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			switch userID {
			case actorID:
				return models.PermissionLevelAdmin, nil
			case targetID:
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		updateServerMemberLevelFn: func(_ context.Context, _, _ string, _ int) error {
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
		changePermissionLevelRequest{PermissionLevel: models.PermissionLevelMod}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "changeRole must emit system message")
	assert.Equal(t, "role_changed", capturedEventType)
	require.NotNil(t, capturedMetadata)
	// metadata contains old_level/new_level integers
	assert.NotNil(t, capturedMetadata["old_level"])
	assert.NotNil(t, capturedMetadata["new_level"])
}

// ---------- createServer template tests ----------

// channelCreation records a CreateChannel call for test assertions.
type channelCreation struct {
	ServerID          string
	EncryptedMetadata []byte
	Type              string
	ParentID          *string
	Position          int
}

// TestCreateServer_Template verifies createServer creates template channels,
// broadcasts channel_created for each, and emits a server_created system message.
func TestCreateServer_Template(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var mu sync.Mutex
	var createdChannels []channelCreation
	var systemMessages []string
	channelCounter := 0

	store := &mockStore{
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return &models.ServerTemplate{
				ID:        uuid.New().String(),
				Name:      "Default",
				IsDefault: true,
				Channels: []models.TemplateChannel{
					{Name: "system", Type: "system", Position: -1},
					{Name: "general", Type: "text", Position: 0},
					{Name: "General", Type: "voice", Position: 1},
				},
			}, nil
		},
		createServerFn: func(_ context.Context, metadata []byte) (*models.Server, error) {
			return &models.Server{ID: serverID, EncryptedMetadata: metadata}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _ string, _ int) error { return nil },
		getChannelByTypeAndPositionFn: func(_ context.Context, _, _ string, _ int) (*models.Channel, error) {
			return nil, nil // no existing channels
		},
		createChannelFn: func(_ context.Context, srvID string, metadata []byte, chType string, parentID *string, position int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			channelCounter++
			createdChannels = append(createdChannels, channelCreation{
				ServerID: srvID, EncryptedMetadata: metadata, Type: chType,
				ParentID: parentID, Position: position,
			})
			sid := srvID
			return &models.Channel{
				ID: "ch-" + chType, ServerID: &sid,
				EncryptedMetadata: metadata, Type: chType, Position: position,
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

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	// Template has 3 channels; wait briefly for the goroutine to run.
	// The goroutine runs after response is sent, so we poll.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(createdChannels)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, createdChannels, 3, "expected 3 template channels to be created")
	assert.Equal(t, "system", createdChannels[0].Type)
	assert.Equal(t, -1, createdChannels[0].Position)
	assert.Equal(t, "text", createdChannels[1].Type)
	assert.Equal(t, 0, createdChannels[1].Position)
	assert.Equal(t, "voice", createdChannels[2].Type)
	assert.Equal(t, 1, createdChannels[2].Position)

	// server_created system message should be emitted
	assert.Contains(t, systemMessages, "server_created")
}

func TestCreateServer_FallbackTemplateSeedsDefaultChannels(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var mu sync.Mutex
	var createdChannels []channelCreation

	store := &mockStore{
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return nil, nil
		},
		createServerFn: func(_ context.Context, metadata []byte) (*models.Server, error) {
			return &models.Server{ID: serverID, EncryptedMetadata: metadata}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _ string, _ int) error { return nil },
		getChannelByTypeAndPositionFn: func(_ context.Context, _, _ string, _ int) (*models.Channel, error) {
			return nil, nil
		},
		createChannelFn: func(_ context.Context, srvID string, metadata []byte, chType string, parentID *string, position int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			createdChannels = append(createdChannels, channelCreation{
				ServerID:          srvID,
				EncryptedMetadata: metadata,
				Type:              chType,
				ParentID:          parentID,
				Position:          position,
			})
			sid := srvID
			return &models.Channel{
				ID:                uuid.New().String(),
				ServerID:          &sid,
				EncryptedMetadata: metadata,
				Type:              chType,
				Position:          position,
			}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			return &models.SystemMessage{ID: uuid.New().String(), EventType: eventType}, nil
		},
	}

	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, token)
	require.Equal(t, http.StatusCreated, rr.Code)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(createdChannels)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, createdChannels, 3, "expected fallback template to create default channels")
	assert.Equal(t, "system", createdChannels[0].Type)
	assert.Equal(t, -1, createdChannels[0].Position)
	assert.Equal(t, "text", createdChannels[1].Type)
	assert.Equal(t, 0, createdChannels[1].Position)
	assert.Equal(t, "voice", createdChannels[2].Type)
	assert.Equal(t, 1, createdChannels[2].Position)
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
		getDefaultServerTemplateFn: func(_ context.Context) (*models.ServerTemplate, error) {
			return &models.ServerTemplate{
				ID: uuid.New().String(), Name: "Default", IsDefault: true,
				Channels: []models.TemplateChannel{
					{Name: "system", Type: "system", Position: -1},
					{Name: "general", Type: "text", Position: 0},
				},
			}, nil
		},
		createServerFn: func(_ context.Context, metadata []byte) (*models.Server, error) {
			return &models.Server{ID: serverID, EncryptedMetadata: metadata}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _ string, _ int) error { return nil },
		getChannelByTypeAndPositionFn: func(_ context.Context, _, _ string, _ int) (*models.Channel, error) {
			return nil, nil
		},
		createChannelFn: func(_ context.Context, _ string, metadata []byte, chType string, _ *string, position int) (*models.Channel, error) {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if callCount == 2 { // fail on second channel
				return nil, errors.New("db error")
			}
			sid := serverID
			return &models.Channel{
				ID: "ch-1", ServerID: &sid, Type: chType, Position: position,
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

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{}`)}, token)
	require.Equal(t, http.StatusCreated, rr.Code, "server creation should still succeed on partial template failure")

	// Wait for goroutine to run.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(systemMessages)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, systemMessages, "template_partial_failure", "partial failure should emit system message")
}
