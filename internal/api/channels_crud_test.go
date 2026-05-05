package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testServerID = "srv-test-1"

// withGuildLevelContext injects userID and guildLevel into the request context,
// simulating what RequireAuth + RequireGuildMember do in the parent ServerRoutes.
// This lets us test ChannelRoutes in isolation without a full chi router stack.
func withGuildLevelContext(userID string, level int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := withUserID(r.Context(), userID)
			ctx = withGuildLevel(ctx, level)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// channelsCrudTestHandler wraps ChannelRoutes with a guild level context middleware.
func channelsCrudTestHandler(store *mockStore, level int) http.Handler {
	userID := "test-user-id"
	inner := ChannelRoutes(store, nil, nil)
	return withGuildLevelContext(userID, level)(inner)
}

// channelsCrudRouter builds a channel routes handler. The guild permission level
// is resolved by calling getServerMemberLevelFn on each request, mirroring
// RequireGuildMember behaviour. Requests are mounted under
// /servers/{serverId}/channels so handlers see the serverId chi URL param.
func channelsCrudRouter(store *mockStore) http.Handler {
	return channelsCrudRouterFor(store, testServerID)
}

// channelsCrudRouterFor lets a test target a specific guild ID, used by the
// cross-guild IDOR regression tests so the URL guild and the channel's actual
// guild can deliberately differ.
func channelsCrudRouterFor(store *mockStore, urlServerID string) http.Handler {
	userID := "test-channel-user-id"
	r := chi.NewRouter()
	r.Route("/servers/{serverId}", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				level, _ := store.GetServerMemberLevel(req.Context(), urlServerID, userID)
				ctx := withUserID(req.Context(), userID)
				ctx = withGuildLevel(ctx, level)
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		r.Mount("/channels", ChannelRoutes(store, nil, nil))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Tests historically posted to "/<channelId>" relative to the channel router.
		// Rewrite the path to mount under the server-scoped route.
		req2 := req.Clone(req.Context())
		req2.URL.Path = "/servers/" + urlServerID + "/channels" + req.URL.Path
		req2.RequestURI = req2.URL.RequestURI()
		r.ServeHTTP(w, req2)
	})
}

func TestCreateChannel_ValidTextChannel_ReturnsChannel(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) {
		return models.PermissionLevelAdmin, nil
	}
	chID := uuid.New().String()
	encMeta := []byte(`{"name":"general"}`)
	store.createChannelFn = func(_ context.Context, serverID string, metadata []byte, chType string, parentID *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "text", chType)
		assert.Equal(t, 0, pos)
		return &models.Channel{ID: chID, EncryptedMetadata: metadata, Type: chType, Position: pos}, nil
	}
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{EncryptedMetadata: encMeta, Type: "text"}, "")
	assert.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&ch))
	assert.Equal(t, "text", ch.Type)
	assert.Equal(t, chID, ch.ID)
}

func TestCreateChannel_ValidVoiceChannel_ReturnsChannel(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) {
		return models.PermissionLevelAdmin, nil
	}
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, _ string, metadata []byte, chType string, _ *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "voice", chType)
		return &models.Channel{ID: chID, EncryptedMetadata: metadata, Type: chType, Position: pos}, nil
	}
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{
		EncryptedMetadata: []byte(`{"name":"voice-1"}`), Type: "voice",
	}, "")
	assert.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&ch))
	assert.Equal(t, "voice", ch.Type)
}

func TestCreateChannel_InvalidType_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{
		EncryptedMetadata: []byte(`{"name":"x"}`), Type: "invalid",
	}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "text, voice, or category")
}

func TestCreateChannel_MemberForbidden_Returns403(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelMember, nil }
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{EncryptedMetadata: []byte(`{}`), Type: "text"}, "")
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestListChannels_ReturnsSortedByPosition(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelMember, nil }
	store.listChannelsFn = func(_ context.Context, _ string) ([]models.Channel, error) {
		return []models.Channel{
			{ID: "ch1", EncryptedMetadata: []byte(`{"name":"general"}`), Type: "text", Position: 0},
			{ID: "ch2", EncryptedMetadata: []byte(`{"name":"random"}`), Type: "text", Position: 1},
		}, nil
	}
	router := channelsCrudRouter(store)
	rr := getServer(router, "/", "")
	assert.Equal(t, http.StatusOK, rr.Code)
	var list []models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list, 2)
	assert.Equal(t, 0, list[0].Position)
	assert.Equal(t, 1, list[1].Position)
}

func TestDeleteChannel_AsAdmin_Returns204(t *testing.T) {
	channelID := uuid.New().String()
	srv := testServerID
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		assert.Equal(t, channelID, chID)
		return &models.Channel{ID: chID, ServerID: &srv, Type: "text"}, nil
	}
	store.deleteChannelFn = func(_ context.Context, chID, srvID string) error {
		assert.Equal(t, channelID, chID)
		assert.Equal(t, srv, srvID)
		return nil
	}
	router := channelsCrudRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/"+channelID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestDeleteChannel_NotAdmin_Returns403(t *testing.T) {
	channelID := uuid.New().String()
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelMember, nil }
	router := channelsCrudRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/"+channelID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestCreateChannel_ValidCategory_ReturnsChannel(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, _ string, metadata []byte, chType string, parentID *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "category", chType)
		assert.Nil(t, parentID)
		return &models.Channel{ID: chID, EncryptedMetadata: metadata, Type: chType, Position: pos}, nil
	}
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{EncryptedMetadata: []byte(`{"name":"General"}`), Type: "category"}, "")
	require.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ch))
	assert.Equal(t, "category", ch.Type)
}

func TestCreateChannel_CategoryWithParentID_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	parentID := uuid.New().String()
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{EncryptedMetadata: []byte(`{}`), Type: "category", ParentID: &parentID}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// --- MoveChannel tests ---

func TestMoveChannel_AdminMovesToCategory_Returns204(t *testing.T) {
	channelID := uuid.New().String()
	categoryID := uuid.New().String()
	srv := testServerID
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == categoryID {
			return &models.Channel{ID: categoryID, ServerID: &srv, Type: "category"}, nil
		}
		if chID == channelID {
			return &models.Channel{ID: channelID, ServerID: &srv, Type: "text"}, nil
		}
		return nil, nil
	}
	store.moveChannelFn = func(_ context.Context, chID, srvID string, parentID *string, pos int) error {
		assert.Equal(t, channelID, chID)
		assert.Equal(t, srv, srvID)
		require.NotNil(t, parentID)
		assert.Equal(t, categoryID, *parentID)
		assert.Equal(t, 2, pos)
		return nil
	}
	router := channelsCrudRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &categoryID, Position: 2}, "")
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestMoveChannel_AdminMovesToUncategorized_Returns204(t *testing.T) {
	channelID := uuid.New().String()
	srv := testServerID
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		return &models.Channel{ID: chID, ServerID: &srv, Type: "text"}, nil
	}
	store.moveChannelFn = func(_ context.Context, chID, srvID string, parentID *string, pos int) error {
		assert.Equal(t, channelID, chID)
		assert.Equal(t, srv, srvID)
		assert.Nil(t, parentID)
		assert.Equal(t, 0, pos)
		return nil
	}
	router := channelsCrudRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, "")
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestMoveChannel_NotAdmin_Returns403(t *testing.T) {
	channelID := uuid.New().String()
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelMember, nil }
	router := channelsCrudRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, "")
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestMoveChannel_ParentNotCategory_Returns400(t *testing.T) {
	channelID := uuid.New().String()
	textChannelID := uuid.New().String()
	srv := testServerID
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) { return models.PermissionLevelAdmin, nil }
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == textChannelID {
			return &models.Channel{ID: textChannelID, ServerID: &srv, Type: "text"}, nil
		}
		if chID == channelID {
			return &models.Channel{ID: channelID, ServerID: &srv, Type: "text"}, nil
		}
		return nil, nil
	}
	router := channelsCrudRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &textChannelID, Position: 0}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "category")
}
