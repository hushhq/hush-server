package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testServerID = "srv-test-1"

// withGuildContext injects userID and guildRole into the request context, simulating
// what RequireAuth + RequireGuildMember do in the parent ServerRoutes. This lets us
// test ChannelRoutes in isolation without a full chi router stack.
func withGuildContext(userID, guildRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := withUserID(r.Context(), userID)
			ctx = withGuildRole(ctx, guildRole)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// channelsCrudTestHandler wraps ChannelRoutes with a guild context middleware for testing.
// guildRole controls what role the requester appears to have.
func channelsCrudTestHandler(store *mockStore, guildRole string) http.Handler {
	userID := "test-user-id"
	inner := ChannelRoutes(store, nil)
	return withGuildContext(userID, guildRole)(inner)
}

// channelsCrudRouter builds a channel routes handler authenticated as an admin.
// Tests that need a different role use channelsCrudTestHandler directly.
func channelsCrudRouter(store *mockStore) http.Handler {
	// The existing tests set getUserRoleFn to control role. We map that to guild role.
	// Since tests always set getServerMemberRoleFn in the new design,
	// we delegate to makeAuth + guildRole injection for the common case.
	//
	// For backward compat with tests that still call channelsCrudRouter, we use
	// a minimal wrapper that calls the store's getServerMemberRoleFn to determine role.
	userID := "test-channel-user-id"
	inner := ChannelRoutes(store, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolve guild role from store (mirrors RequireGuildMember behavior).
		role, _ := store.GetServerMemberRole(r.Context(), testServerID, userID)
		ctx := withUserID(r.Context(), userID)
		ctx = withGuildRole(ctx, role)
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestCreateChannel_ValidTextChannel_ReturnsChannel(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) {
		return "admin", nil
	}
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, serverID, name, chType string, voiceMode *string, parentID *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "general", name)
		assert.Equal(t, "text", chType)
		assert.Nil(t, voiceMode)
		assert.Equal(t, 0, pos)
		return &models.Channel{ID: chID, Name: name, Type: chType, Position: pos}, nil
	}
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{Name: "general", Type: "text"}, "")
	assert.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&ch))
	assert.Equal(t, "general", ch.Name)
	assert.Equal(t, "text", ch.Type)
	assert.Equal(t, chID, ch.ID)
}

func TestCreateChannel_ValidVoiceChannel_ReturnsChannel(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) {
		return "admin", nil
	}
	perf := "low-latency"
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, _, name, chType string, voiceMode *string, _ *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "voice", chType)
		require.NotNil(t, voiceMode)
		assert.Equal(t, "low-latency", *voiceMode)
		return &models.Channel{ID: chID, Name: name, Type: chType, VoiceMode: voiceMode, Position: pos}, nil
	}
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{
		Name: "voice-1", Type: "voice", VoiceMode: &perf,
	}, "")
	assert.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&ch))
	assert.Equal(t, "voice", ch.Type)
	assert.Equal(t, "low-latency", *ch.VoiceMode)
}

func TestCreateChannel_VoiceModeOnTextChannel_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	perf := "low-latency"
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{
		Name: "general", Type: "text", VoiceMode: &perf,
	}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "voice_mode")
}

func TestCreateChannel_MissingVoiceModeOnVoice_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{
		Name: "voice-1", Type: "voice",
	}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "voice_mode")
}

func TestCreateChannel_InvalidType_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{
		Name: "x", Type: "invalid",
	}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "text, voice, or category")
}

func TestCreateChannel_MemberForbidden_Returns403(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "member", nil }
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{Name: "general", Type: "text"}, "")
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestListChannels_ReturnsSortedByPosition(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "member", nil }
	store.listChannelsFn = func(_ context.Context, _ string) ([]models.Channel, error) {
		return []models.Channel{
			{ID: "ch1", Name: "general", Type: "text", Position: 0},
			{ID: "ch2", Name: "random", Type: "text", Position: 1},
		}, nil
	}
	router := channelsCrudRouter(store)
	rr := getServer(router, "/", "")
	assert.Equal(t, http.StatusOK, rr.Code)
	var list []models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list, 2)
	assert.Equal(t, "general", list[0].Name)
	assert.Equal(t, 0, list[0].Position)
	assert.Equal(t, "random", list[1].Name)
}

func TestDeleteChannel_AsAdmin_Returns204(t *testing.T) {
	channelID := uuid.New().String()
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	store.deleteChannelFn = func(_ context.Context, chID string) error {
		assert.Equal(t, channelID, chID)
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
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "member", nil }
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
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, _, name, chType string, voiceMode *string, parentID *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "category", chType)
		assert.Nil(t, voiceMode)
		assert.Nil(t, parentID)
		return &models.Channel{ID: chID, Name: name, Type: chType, Position: pos}, nil
	}
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{Name: "General", Type: "category"}, "")
	require.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ch))
	assert.Equal(t, "category", ch.Type)
}

func TestCreateChannel_CategoryWithVoiceMode_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	vm := "quality"
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{Name: "cat", Type: "category", VoiceMode: &vm}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateChannel_CategoryWithParentID_Returns400(t *testing.T) {
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	parentID := uuid.New().String()
	router := channelsCrudRouter(store)
	rr := postServerJSON(router, "/", models.CreateChannelRequest{Name: "cat", Type: "category", ParentID: &parentID}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// --- MoveChannel tests ---

func TestMoveChannel_AdminMovesToCategory_Returns204(t *testing.T) {
	channelID := uuid.New().String()
	categoryID := uuid.New().String()
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == categoryID {
			return &models.Channel{ID: categoryID, Type: "category"}, nil
		}
		return nil, nil
	}
	store.moveChannelFn = func(_ context.Context, chID string, parentID *string, pos int) error {
		assert.Equal(t, channelID, chID)
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
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	store.moveChannelFn = func(_ context.Context, chID string, parentID *string, pos int) error {
		assert.Equal(t, channelID, chID)
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
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "member", nil }
	router := channelsCrudRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, "")
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestMoveChannel_ParentNotCategory_Returns400(t *testing.T) {
	channelID := uuid.New().String()
	textChannelID := uuid.New().String()
	store := &mockStore{}
	store.getServerMemberRoleFn = func(_ context.Context, _, _ string) (string, error) { return "admin", nil }
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == textChannelID {
			return &models.Channel{ID: textChannelID, Type: "text"}, nil
		}
		return nil, nil
	}
	router := channelsCrudRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &textChannelID, Position: 0}, "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "category")
}
