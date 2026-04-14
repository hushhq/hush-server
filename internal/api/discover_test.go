package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/hushhq/hush-server/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userCapture is a hub stub that records BroadcastToUser calls.
// Named distinctly from mockHub (moderation_test.go) to avoid redeclaration.
type userCapture struct {
	toUser []userBroadcast
}

type userBroadcast struct {
	userID  string
	message []byte
}

func (h *userCapture) BroadcastToAll(_ []byte)                     {}
func (h *userCapture) BroadcastToServer(_ string, _ []byte)        {}
func (h *userCapture) BroadcastToUser(uid string, msg []byte)      { h.toUser = append(h.toUser, userBroadcast{uid, msg}) }
func (h *userCapture) DisconnectUser(_ string)                     {}

func discoverRouter(store *mockStore, hub GlobalBroadcaster) http.Handler {
	return GuildRoutes(store, hub, testJWTSecret)
}

func postDiscoverJSON(handler http.Handler, path string, body interface{}, token string) *httptest.ResponseRecorder {
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getDiscover(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ──────────────────────────────────────────────────────────────────────────────
// POST /dm
// ──────────────────────────────────────────────────────────────────────────────

// TestCreateOrFindDM_New_BroadcastsToRecipient verifies that creating a brand-new
// DM guild notifies the OTHER user via BroadcastToUser, not BroadcastToServer.
//
// The core reliability guarantee: the recipient's guild list refreshes immediately
// because BroadcastToUser routes by userID on the existing WS connection, bypassing
// server-level subscriptions that don't exist yet for the newly-created guild.
func TestCreateOrFindDM_New_BroadcastsToRecipient(t *testing.T) {
	callerID := uuid.New().String()
	otherID := uuid.New().String()
	serverID := uuid.New().String()
	channelID := uuid.New().String()

	store := &mockStore{
		getUserByIDFn: func(_ context.Context, id string) (*models.User, error) {
			if id != otherID {
				return nil, nil
			}
			return &models.User{ID: otherID, Username: "bob", DisplayName: "Bob"}, nil
		},
		findDMGuildFn: func(_ context.Context, _, _ string) (*models.Server, error) {
			return nil, pgx.ErrNoRows
		},
		createDMGuildFn: func(_ context.Context, _, _ string) (*models.Server, *models.Channel, error) {
			s := &models.Server{ID: serverID, IsDm: true, AccessPolicy: "closed", MemberCount: 2, TextChannelCount: 1}
			ch := &models.Channel{ID: channelID, ServerID: &serverID, Type: "text"}
			return s, ch, nil
		},
	}

	hub := &userCapture{}
	token := makeAuth(store, callerID)
	rr := postDiscoverJSON(discoverRouter(store, hub), "/dm", models.CreateDMRequest{OtherUserID: otherID}, token)

	require.Equal(t, http.StatusCreated, rr.Code)

	var resp models.DMResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, serverID, resp.Server.ID)
	assert.Equal(t, channelID, resp.ChannelID)
	assert.Equal(t, otherID, resp.OtherUser.ID)

	// Critical assertion: recipient received the member_joined event.
	require.Len(t, hub.toUser, 1, "BroadcastToUser must be called exactly once for the new DM")
	assert.Equal(t, otherID, hub.toUser[0].userID, "event must target the recipient, not the caller")

	var event map[string]interface{}
	require.NoError(t, json.Unmarshal(hub.toUser[0].message, &event))
	assert.Equal(t, "member_joined", event["type"])
	assert.Equal(t, serverID, event["server_id"])
	assert.Equal(t, callerID, event["user_id"])
}

// TestCreateOrFindDM_Existing_NoBroadcast verifies that retrieving an already-existing
// DM returns 200 and does NOT emit another member_joined event.
func TestCreateOrFindDM_Existing_NoBroadcast(t *testing.T) {
	callerID := uuid.New().String()
	otherID := uuid.New().String()
	serverID := uuid.New().String()
	channelID := uuid.New().String()
	sid := serverID

	store := &mockStore{
		getUserByIDFn: func(_ context.Context, id string) (*models.User, error) {
			if id != otherID {
				return nil, nil
			}
			return &models.User{ID: otherID, Username: "bob", DisplayName: "Bob"}, nil
		},
		findDMGuildFn: func(_ context.Context, _, _ string) (*models.Server, error) {
			return &models.Server{ID: serverID, IsDm: true, AccessPolicy: "closed"}, nil
		},
		listChannelsFn: func(_ context.Context, _ string) ([]models.Channel, error) {
			return []models.Channel{{ID: channelID, ServerID: &sid, Type: "text"}}, nil
		},
	}

	hub := &userCapture{}
	token := makeAuth(store, callerID)
	rr := postDiscoverJSON(discoverRouter(store, hub), "/dm", models.CreateDMRequest{OtherUserID: otherID}, token)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, hub.toUser, 0, "no broadcast for an already-existing DM")
}

// TestCreateOrFindDM_SelfDM_Rejected verifies that attempting to DM yourself returns 400.
func TestCreateOrFindDM_SelfDM_Rejected(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	rr := postDiscoverJSON(discoverRouter(store, nil), "/dm", models.CreateDMRequest{OtherUserID: userID}, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestCreateOrFindDM_OtherUserNotFound verifies that DM creation with a non-existent
// user ID returns 404.
func TestCreateOrFindDM_OtherUserNotFound(t *testing.T) {
	callerID := uuid.New().String()
	store := &mockStore{
		getUserByIDFn: func(_ context.Context, _ string) (*models.User, error) {
			return nil, nil
		},
	}
	token := makeAuth(store, callerID)
	rr := postDiscoverJSON(discoverRouter(store, nil), "/dm", models.CreateDMRequest{OtherUserID: uuid.New().String()}, token)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// TestCreateOrFindDM_NoAuth verifies that unauthenticated requests are rejected.
func TestCreateOrFindDM_NoAuth(t *testing.T) {
	store := &mockStore{}
	rr := postDiscoverJSON(discoverRouter(store, nil), "/dm", models.CreateDMRequest{OtherUserID: uuid.New().String()}, "")
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

// TestCreateOrFindDM_MissingBody verifies that a request with no body returns 400.
func TestCreateOrFindDM_MissingBody(t *testing.T) {
	callerID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, callerID)
	rr := postDiscoverJSON(discoverRouter(store, nil), "/dm", models.CreateDMRequest{OtherUserID: ""}, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// GET /discover
// ──────────────────────────────────────────────────────────────────────────────

func TestDiscoverGuilds_ReturnsList(t *testing.T) {
	callerID := uuid.New().String()
	now := time.Now()
	guilds := []models.DiscoverGuild{
		{ID: uuid.New().String(), PublicName: "Gaming Hub", Category: "gaming", AccessPolicy: "open", MemberCount: 42, CreatedAt: now},
	}
	store := &mockStore{
		discoverGuildsFn: func(_ context.Context, category, search, sort string, page, pageSize int) ([]models.DiscoverGuild, int, error) {
			assert.Equal(t, "members", sort)
			assert.Equal(t, 1, page)
			return guilds, 1, nil
		},
	}
	token := makeAuth(store, callerID)
	rr := getDiscover(discoverRouter(store, nil), "/discover", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	list, ok := body["guilds"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 1)
	assert.Equal(t, float64(1), body["total"])
}

func TestDiscoverGuilds_InvalidPage(t *testing.T) {
	callerID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, callerID)
	rr := getDiscover(discoverRouter(store, nil), "/discover?page=abc", token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDiscoverGuilds_PageSizeCapped(t *testing.T) {
	callerID := uuid.New().String()
	var capturedPageSize int
	store := &mockStore{
		discoverGuildsFn: func(_ context.Context, _, _, _ string, _, pageSize int) ([]models.DiscoverGuild, int, error) {
			capturedPageSize = pageSize
			return []models.DiscoverGuild{}, 0, nil
		},
	}
	token := makeAuth(store, callerID)
	rr := getDiscover(discoverRouter(store, nil), "/discover?pageSize=999", token)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, discoverPageSizeMax, capturedPageSize, "pageSize must be capped at discoverPageSizeMax")
}

// ──────────────────────────────────────────────────────────────────────────────
// GET /users/search
// ──────────────────────────────────────────────────────────────────────────────

func TestSearchUsers_Returns(t *testing.T) {
	callerID := uuid.New().String()
	results := []models.UserSearchPublicResult{
		{ID: uuid.New().String(), Username: "alice", DisplayName: "Alice"},
	}
	store := &mockStore{
		searchUsersPublicFn: func(_ context.Context, query string, limit int) ([]models.UserSearchPublicResult, error) {
			assert.Equal(t, "al", query)
			assert.Equal(t, searchUsersLimit, limit)
			return results, nil
		},
	}
	token := makeAuth(store, callerID)
	rr := getDiscover(discoverRouter(store, nil), "/users/search?q=al", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var body []models.UserSearchPublicResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	require.Len(t, body, 1)
	assert.Equal(t, "alice", body[0].Username)
}

func TestSearchUsers_QueryTooShort(t *testing.T) {
	callerID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, callerID)
	rr := getDiscover(discoverRouter(store, nil), "/users/search?q=a", token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSearchUsers_NoAuth(t *testing.T) {
	store := &mockStore{}
	rr := getDiscover(discoverRouter(store, nil), "/users/search?q=al", "")
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}
