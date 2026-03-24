package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- GET /{serverId}/system-messages ----------

func TestListSystemMessages_Empty(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		listSystemMessagesFn: func(_ context.Context, _ string, _ time.Time, _ int) ([]models.SystemMessage, error) {
			return nil, nil // empty
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/"+serverID+"/system-messages", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var msgs []models.SystemMessage
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&msgs))
	assert.Empty(t, msgs)
}

func TestListSystemMessages_WithMessages(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	now := time.Now()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		listSystemMessagesFn: func(_ context.Context, srvID string, _ time.Time, limit int) ([]models.SystemMessage, error) {
			assert.Equal(t, serverID, srvID)
			assert.Equal(t, 50, limit)
			return []models.SystemMessage{
				{ID: "sm-1", ServerID: serverID, EventType: "member_joined", ActorID: userID, CreatedAt: now},
				{ID: "sm-2", ServerID: serverID, EventType: "member_left", ActorID: userID, CreatedAt: now.Add(-time.Minute)},
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := getServer(router, "/"+serverID+"/system-messages", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var msgs []models.SystemMessage
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&msgs))
	require.Len(t, msgs, 2)
	assert.Equal(t, "member_joined", msgs[0].EventType)
	assert.Equal(t, "member_left", msgs[1].EventType)
}

// ---------- System channel protections ----------

func TestSystemChannel_DeleteBlocked(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	channelID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelAdmin, nil
			}
			return 0, errors.New("not a member")
		},
		getChannelByIDFn: func(_ context.Context, chID string) (*models.Channel, error) {
			if chID == channelID {
				return &models.Channel{ID: channelID, ServerID: &serverID, Type: "system"}, nil
			}
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := deleteServer(router, "/"+serverID+"/channels/"+channelID, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "system channels cannot be deleted")
}

func TestSystemChannel_MoveBlocked(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	channelID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelAdmin, nil
			}
			return 0, errors.New("not a member")
		},
		getChannelByIDFn: func(_ context.Context, chID string) (*models.Channel, error) {
			if chID == channelID {
				return &models.Channel{ID: channelID, ServerID: &serverID, Type: "system"}, nil
			}
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := putServerJSON(router, "/"+serverID+"/channels/"+channelID+"/move",
		models.MoveChannelRequest{Position: 5}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "system channels cannot be moved")
}

// ---------- POST /{serverId}/leave ----------

func TestLeaveServer_Success(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var removed bool
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelMember, nil
			}
			return 0, errors.New("not a member")
		},
		removeServerMemberFn: func(_ context.Context, srvID, uid string) error {
			assert.Equal(t, serverID, srvID)
			assert.Equal(t, userID, uid)
			removed = true
			return nil
		},
	}
	hub := &mockHub{}
	token := makeAuth(store, userID)
	router := serversRouterWithHub(store, hub)

	rr := postServerJSON(router, "/"+serverID+"/leave", nil, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, removed, "member must be removed")

	// Should broadcast member_left
	require.GreaterOrEqual(t, len(hub.broadcastCalls), 1, "must broadcast member_left")
	var bcPayload map[string]interface{}
	require.NoError(t, json.Unmarshal(hub.broadcastCalls[0].message, &bcPayload))
	assert.Equal(t, "member_left", bcPayload["type"])
}

func TestLeaveServer_OwnerBlocked(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, uid string) (int, error) {
			if uid == userID {
				return models.PermissionLevelOwner, nil
			}
			return 0, errors.New("not a member")
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/"+serverID+"/leave", nil, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "owner cannot leave")
}

// ---------- POST / (createServer) creates #system channel ----------

func TestCreateServer_CreatesSystemChannel(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()

	var systemChannelCreated bool
	store := &mockStore{
		createServerFn: func(_ context.Context, metadata []byte) (*models.Server, error) {
			return &models.Server{ID: serverID, EncryptedMetadata: metadata}, nil
		},
		addServerMemberFn: func(_ context.Context, _, _ string, _ int) error { return nil },
		createChannelFn: func(_ context.Context, srvID string, meta []byte, channelType string, _ *string, _ *string, position int) (*models.Channel, error) {
			if channelType == "system" {
				assert.Equal(t, serverID, srvID)
				assert.Equal(t, -1, position)
				systemChannelCreated = true
			}
			return &models.Channel{ID: uuid.New().String(), ServerID: &srvID, Type: channelType, Position: position}, nil
		},
	}
	token := makeAuth(store, userID)
	router := serversRouter(store)

	rr := postServerJSON(router, "/", models.CreateServerRequest{EncryptedMetadata: []byte(`{"name":"Test Guild"}`)}, token)
	require.Equal(t, http.StatusCreated, rr.Code)
	assert.True(t, systemChannelCreated, "createServer must create a #system channel")
}
