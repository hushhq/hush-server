package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// livekitRouter wires LiveKitRoutes with a real apiKey/apiSecret so token
// generation succeeds.  The handler uses "test-key"/"test-secret" which are
// recognised by the HMAC-based JWT signer regardless of a live LiveKit server.
func livekitRouter(store *mockStore) http.Handler {
	return LiveKitRoutes(store, testJWTSecret, "test-key", "test-secret")
}

// postLiveKitToken is a helper for POST /token.
func postLiveKitToken(handler http.Handler, roomName, participantName, token string) *httptest.ResponseRecorder {
	body := map[string]string{
		"roomName":        roomName,
		"participantName": participantName,
	}
	return postServerJSON(handler, "/token", body, token)
}

// ---------- Muted user tests ----------

// TestLiveKitToken_MutedUser verifies that a muted user receives 403 with
// code "muted" when requesting a LiveKit token for a channel room.
func TestLiveKitToken_MutedUser(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	roomName := "channel-" + channelID

	store := &mockStore{
		getChannelByIDFn: func(_ context.Context, id string) (*models.Channel, error) {
			if id == channelID {
				return &models.Channel{
					ID:       channelID,
					// Name field removed - channel names are in EncryptedMetadata.
					Type:     "voice",
					ServerID: ptrString(serverID),
				}, nil
			}
			return nil, nil
		},
		getActiveMuteFn: func(_ context.Context, sid, uid string) (*models.Mute, error) {
			if sid == serverID && uid == userID {
				return &models.Mute{
					ID:       uuid.New().String(),
					ServerID: ptrString(serverID),
					UserID:   userID,
					ActorID:  uuid.New().String(),
					Reason:   "disruptive",
				}, nil
			}
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	router := livekitRouter(store)

	rr := postLiveKitToken(router, roomName, "Alice", token)

	require.Equal(t, http.StatusForbidden, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "muted", body["code"])
	assert.Contains(t, body["error"], "muted")
}

// TestLiveKitToken_NonMutedUser verifies that a non-muted user receives a
// LiveKit token successfully.
func TestLiveKitToken_NonMutedUser(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	roomName := "channel-" + channelID

	store := &mockStore{
		getChannelByIDFn: func(_ context.Context, id string) (*models.Channel, error) {
			if id == channelID {
				return &models.Channel{
					ID:       channelID,
					// Name field removed - channel names are in EncryptedMetadata.
					Type:     "voice",
					ServerID: ptrString(serverID),
				}, nil
			}
			return nil, nil
		},
		getActiveMuteFn: func(_ context.Context, _, _ string) (*models.Mute, error) {
			// No active mute
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	router := livekitRouter(store)

	rr := postLiveKitToken(router, roomName, "Alice", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.NotEmpty(t, body["token"], "token must be present in 200 response")
}

// TestLiveKitToken_NoChannelPrefix verifies that a roomName without the
// "channel-" prefix skips the mute check and returns a token.
func TestLiveKitToken_NoChannelPrefix(t *testing.T) {
	userID := uuid.New().String()
	roomName := "custom-room"

	store := &mockStore{
		// getChannelByIDFn and getActiveMuteFn are intentionally nil to confirm
		// they are never called for non-channel roomNames.
	}
	token := makeAuth(store, userID)
	router := livekitRouter(store)

	rr := postLiveKitToken(router, roomName, "Bob", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.NotEmpty(t, body["token"])
}

// ---------- Ban / membership gating ----------

// TestLiveKitToken_GuildBannedUser proves a user with an active per-
// guild ban is denied a voice token for that guild's channels with
// a precise "banned" code so the client can render the right copy.
func TestLiveKitToken_GuildBannedUser(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	roomName := "channel-" + channelID

	store := &mockStore{
		getChannelByIDFn: func(_ context.Context, id string) (*models.Channel, error) {
			if id == channelID {
				return &models.Channel{ID: channelID, Type: "voice", ServerID: ptrString(serverID)}, nil
			}
			return nil, nil
		},
		getActiveBanFn: func(_ context.Context, sid, uid string) (*models.Ban, error) {
			if sid == serverID && uid == userID {
				return &models.Ban{ID: uuid.New().String(), ServerID: ptrString(serverID), UserID: userID, Reason: "x"}, nil
			}
			return nil, nil
		},
	}
	token := makeAuth(store, userID)
	rr := postLiveKitToken(livekitRouter(store), roomName, "Alice", token)

	require.Equal(t, http.StatusForbidden, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "banned", body["code"])
}

// TestLiveKitToken_InstanceBannedUser proves an instance ban blocks
// voice access for any room, even rooms unrelated to a guild
// (no "channel-" prefix).
func TestLiveKitToken_InstanceBannedUser(t *testing.T) {
	userID := uuid.New().String()

	store := &mockStore{
		getActiveInstanceBanFn: func(_ context.Context, uid string) (*models.InstanceBan, error) {
			if uid == userID {
				return &models.InstanceBan{ID: uuid.New().String(), UserID: userID, Reason: "x"}, nil
			}
			return nil, nil
		},
	}
	token := makeAuth(store, userID)

	rr := postLiveKitToken(livekitRouter(store), "channel-anything", "Alice", token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "instance_banned", body["code"])

	rr2 := postLiveKitToken(livekitRouter(store), "custom-room", "Alice", token)
	require.Equal(t, http.StatusForbidden, rr2.Code)
	var body2 map[string]string
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&body2))
	assert.Equal(t, "instance_banned", body2["code"])
}

// TestLiveKitToken_NonMember proves a user who is no longer in the
// guild member roster (e.g. kicked, or never joined) cannot mint a
// voice token for that guild's channel.
func TestLiveKitToken_NonMember(t *testing.T) {
	userID := uuid.New().String()
	channelID := uuid.New().String()
	serverID := uuid.New().String()
	roomName := "channel-" + channelID

	store := &mockStore{
		getChannelByIDFn: func(_ context.Context, id string) (*models.Channel, error) {
			if id == channelID {
				return &models.Channel{ID: channelID, Type: "voice", ServerID: ptrString(serverID)}, nil
			}
			return nil, nil
		},
		// No active ban, no instance ban.
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return 0, errNotFoundLikeMember
		},
	}
	token := makeAuth(store, userID)
	rr := postLiveKitToken(livekitRouter(store), roomName, "Alice", token)

	require.Equal(t, http.StatusForbidden, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "not_member", body["code"])
}

// errNotFoundLikeMember matches the shape of the real "no such
// member" sentinel without coupling the test to db internals: any
// non-nil error from GetServerMemberLevel must fail closed.
var errNotFoundLikeMember = &mockNotFoundErr{msg: "not a member"}

type mockNotFoundErr struct{ msg string }

func (e *mockNotFoundErr) Error() string { return e.msg }
