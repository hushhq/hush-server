package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cross-guild IDOR regression tests. Pinned by ans21.md F1 + F2.
// Each test exercises an attacker-shaped request: the URL is a guild the
// caller has admin/mod role in, but the target object lives in a different
// guild. Expectation: 404, never 204, and no mutation issued to the data
// layer (verified by tripping a t.Fatal inside the mutation mock fn).

func TestF1_DeleteChannel_CrossGuild_Returns404(t *testing.T) {
	channelID := uuid.New().String()
	urlGuild := "guild-A"
	channelHomeGuild := "guild-B"

	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) {
		return models.PermissionLevelAdmin, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		assert.Equal(t, channelID, chID)
		return &models.Channel{ID: chID, ServerID: &channelHomeGuild, Type: "text"}, nil
	}
	store.deleteChannelFn = func(_ context.Context, _, _ string) error {
		t.Fatalf("DeleteChannel must not be reached on cross-guild attempt")
		return nil
	}

	router := channelsCrudRouterFor(store, urlGuild)
	req := httptest.NewRequest(http.MethodDelete, "/"+channelID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestF1_MoveChannel_CrossGuildTarget_Returns404(t *testing.T) {
	channelID := uuid.New().String()
	urlGuild := "guild-A"
	channelHomeGuild := "guild-B"

	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) {
		return models.PermissionLevelAdmin, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		assert.Equal(t, channelID, chID)
		return &models.Channel{ID: chID, ServerID: &channelHomeGuild, Type: "text"}, nil
	}
	store.moveChannelFn = func(_ context.Context, _, _ string, _ *string, _ int) error {
		t.Fatalf("MoveChannel must not be reached on cross-guild attempt")
		return nil
	}

	router := channelsCrudRouterFor(store, urlGuild)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, "")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestF1_MoveChannel_CrossGuildParent_Returns404(t *testing.T) {
	channelID := uuid.New().String()
	parentID := uuid.New().String()
	urlGuild := "guild-A"

	urlGuildPtr := urlGuild
	parentHomeGuild := "guild-B"

	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) {
		return models.PermissionLevelAdmin, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		switch chID {
		case channelID:
			return &models.Channel{ID: chID, ServerID: &urlGuildPtr, Type: "text"}, nil
		case parentID:
			return &models.Channel{ID: chID, ServerID: &parentHomeGuild, Type: "category"}, nil
		}
		return nil, nil
	}
	store.moveChannelFn = func(_ context.Context, _, _ string, _ *string, _ int) error {
		t.Fatalf("MoveChannel must not be reached when parent lives in another guild")
		return nil
	}

	router := channelsCrudRouterFor(store, urlGuild)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &parentID, Position: 0}, "")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestF2_DeleteMessage_CrossGuild_Returns404(t *testing.T) {
	actorID := uuid.New().String()
	ownerID := uuid.New().String()
	msgID := uuid.New().String()
	urlGuild := "guild-A"
	channelHomeGuild := "guild-B"

	store := &mockStore{}
	store.getMessageByIDFn = func(_ context.Context, messageID string) (*models.Message, error) {
		require.Equal(t, msgID, messageID)
		return &models.Message{ID: messageID, SenderID: &ownerID, ChannelID: "ch-foreign"}, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		assert.Equal(t, "ch-foreign", chID)
		return &models.Channel{ID: chID, ServerID: &channelHomeGuild, Type: "text"}, nil
	}
	store.deleteMessageFn = func(_ context.Context, _, _ string) error {
		t.Fatalf("DeleteMessage must not be reached on cross-guild attempt")
		return nil
	}

	router := buildModerationRouterFor(store, actorID, "mod", urlGuild)
	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), "")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// Same-guild happy paths still work — guards against the fix over-rejecting.

func TestF1_DeleteChannel_SameGuild_StillSucceeds(t *testing.T) {
	channelID := uuid.New().String()
	srv := testServerID

	deleted := false
	store := &mockStore{}
	store.getServerMemberLevelFn = func(_ context.Context, _, _ string) (int, error) {
		return models.PermissionLevelAdmin, nil
	}
	store.getChannelByIDFn = func(_ context.Context, _ string) (*models.Channel, error) {
		return &models.Channel{ID: channelID, ServerID: &srv, Type: "text"}, nil
	}
	store.deleteChannelTreeFn = func(_ context.Context, _, srvID string) ([]string, []string, error) {
		assert.Equal(t, srv, srvID)
		deleted = true
		return []string{channelID}, nil, nil
	}

	router := channelsCrudRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/"+channelID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// Channel delete now returns 200 with `deletedChannelIds` so the
	// frontend can apply an optimistic local removal that survives a
	// flapping WS subscription.
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, deleted, "same-guild happy path must still reach DeleteChannelTree")
}

func TestF2_DeleteMessage_SameGuild_StillSucceeds(t *testing.T) {
	actorID := uuid.New().String()
	ownerID := uuid.New().String()
	msgID := uuid.New().String()
	srv := testServerID

	deleted := false
	store := &mockStore{}
	store.getMessageByIDFn = func(_ context.Context, messageID string) (*models.Message, error) {
		return &models.Message{ID: messageID, SenderID: &ownerID, ChannelID: "ch-1"}, nil
	}
	store.getChannelByIDFn = func(_ context.Context, _ string) (*models.Channel, error) {
		return &models.Channel{ID: "ch-1", ServerID: &srv, Type: "text"}, nil
	}
	store.deleteMessageFn = func(_ context.Context, _, srvID string) error {
		assert.Equal(t, srv, srvID)
		deleted = true
		return nil
	}

	router := buildModerationRouter(store, actorID, "mod")
	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), "")

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, deleted, "same-guild happy path must still reach DeleteMessage")
}
