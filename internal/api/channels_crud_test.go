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

func serversRouterForChannels(store *mockStore) http.Handler {
	return ServerRoutes(store, testJWTSecret)
}

func TestCreateChannel_ValidTextChannel_ReturnsChannel(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, sid, name, chType string, voiceMode *string, parentID *string, pos int) (*models.Channel, error) {
		assert.Equal(t, serverID, sid)
		assert.Equal(t, "general", name)
		assert.Equal(t, "text", chType)
		assert.Nil(t, voiceMode)
		assert.Equal(t, 0, pos)
		return &models.Channel{ID: chID, ServerID: sid, Name: name, Type: chType, Position: pos}, nil
	}
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{Name: "general", Type: "text"}, token)
	assert.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&ch))
	assert.Equal(t, "general", ch.Name)
	assert.Equal(t, "text", ch.Type)
	assert.Equal(t, chID, ch.ID)
}

func TestCreateChannel_ValidVoiceChannel_ReturnsChannel(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	perf := "low-latency"
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, sid, name, chType string, voiceMode *string, _ *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "voice", chType)
		require.NotNil(t, voiceMode)
		assert.Equal(t, "low-latency", *voiceMode)
		return &models.Channel{ID: chID, ServerID: sid, Name: name, Type: chType, VoiceMode: voiceMode, Position: pos}, nil
	}
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{
		Name: "voice-1", Type: "voice", VoiceMode: &perf,
	}, token)
	assert.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&ch))
	assert.Equal(t, "voice", ch.Type)
	assert.Equal(t, "low-latency", *ch.VoiceMode)
}

func TestCreateChannel_VoiceModeOnTextChannel_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	perf := "low-latency"
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{
		Name: "general", Type: "text", VoiceMode: &perf,
	}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "voice_mode")
}

func TestCreateChannel_MissingVoiceModeOnVoice_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{
		Name: "voice-1", Type: "voice",
	}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "voice_mode")
}

func TestCreateChannel_InvalidType_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{
		Name: "x", Type: "invalid",
	}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "text, voice, or category")
}

func TestCreateChannel_NotAdmin_Returns403(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "member"}, nil
		}
		return nil, nil
	}
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{Name: "general", Type: "text"}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestListChannels_ReturnsSortedByPosition(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{}, nil
		}
		return nil, nil
	}
	store.listChannelsFn = func(_ context.Context, sid string) ([]models.Channel, error) {
		if sid == serverID {
			return []models.Channel{
				{ID: "ch1", ServerID: serverID, Name: "general", Type: "text", Position: 0},
				{ID: "ch2", ServerID: serverID, Name: "random", Type: "text", Position: 1},
			}, nil
		}
		return nil, nil
	}
	router := serversRouterForChannels(store)
	rr := getServer(router, "/"+serverID+"/channels", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var list []models.Channel
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list, 2)
	assert.Equal(t, "general", list[0].Name)
	assert.Equal(t, 0, list[0].Position)
	assert.Equal(t, "random", list[1].Name)
}

func TestDeleteChannel_AsAdmin_Returns204(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	store.deleteChannelFn = func(_ context.Context, chID string) error {
		assert.Equal(t, channelID, chID)
		return nil
	}
	router := channelsRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/"+channelID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestDeleteChannel_NotAdmin_Returns403(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "member"}, nil
		}
		return nil, nil
	}
	router := channelsRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/"+channelID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestCreateChannel_ValidCategory_ReturnsChannel(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	chID := uuid.New().String()
	store.createChannelFn = func(_ context.Context, sid, name, chType string, voiceMode *string, parentID *string, pos int) (*models.Channel, error) {
		assert.Equal(t, "category", chType)
		assert.Nil(t, voiceMode)
		assert.Nil(t, parentID)
		return &models.Channel{ID: chID, ServerID: sid, Name: name, Type: chType, Position: pos}, nil
	}
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{Name: "General", Type: "category"}, token)
	require.Equal(t, http.StatusCreated, rr.Code)
	var ch models.Channel
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ch))
	assert.Equal(t, "category", ch.Type)
}

func TestCreateChannel_CategoryWithVoiceMode_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		return &models.ServerMember{Role: "admin"}, nil
	}
	vm := "quality"
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{Name: "cat", Type: "category", VoiceMode: &vm}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateChannel_CategoryWithParentID_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		return &models.ServerMember{Role: "admin"}, nil
	}
	parentID := uuid.New().String()
	router := serversRouterForChannels(store)
	rr := postServerJSON(router, "/"+serverID+"/channels", models.CreateChannelRequest{Name: "cat", Type: "category", ParentID: &parentID}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// --- MoveChannel tests ---

func TestMoveChannel_AdminMovesToCategory_Returns204(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	categoryID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == categoryID {
			return &models.Channel{ID: categoryID, ServerID: serverID, Type: "category"}, nil
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
	router := channelsRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &categoryID, Position: 2}, token)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestMoveChannel_AdminMovesToUncategorized_Returns204(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	store.moveChannelFn = func(_ context.Context, chID string, parentID *string, pos int) error {
		assert.Equal(t, channelID, chID)
		assert.Nil(t, parentID)
		assert.Equal(t, 0, pos)
		return nil
	}
	router := channelsRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, token)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestMoveChannel_NotAdmin_Returns403(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "member"}, nil
		}
		return nil, nil
	}
	router := channelsRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestMoveChannel_InvalidChannel_Returns404(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		return "", nil
	}
	router := channelsRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{Position: 0}, token)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestMoveChannel_CrossServerParent_Returns400(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	otherServerID := uuid.New().String()
	categoryID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == categoryID {
			return &models.Channel{ID: categoryID, ServerID: otherServerID, Type: "category"}, nil
		}
		return nil, nil
	}
	router := channelsRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &categoryID, Position: 0}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "different server")
}

func TestMoveChannel_ParentNotCategory_Returns400(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	textChannelID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerIDForChannelFn = func(_ context.Context, chID string) (string, error) {
		if chID == channelID {
			return serverID, nil
		}
		return "", nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{Role: "admin"}, nil
		}
		return nil, nil
	}
	store.getChannelByIDFn = func(_ context.Context, chID string) (*models.Channel, error) {
		if chID == textChannelID {
			return &models.Channel{ID: textChannelID, ServerID: serverID, Type: "text"}, nil
		}
		return nil, nil
	}
	router := channelsRouter(store)
	rr := putServerJSON(router, "/"+channelID+"/move", models.MoveChannelRequest{ParentID: &textChannelID, Position: 0}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "category")
}
