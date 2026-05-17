package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internalauth "github.com/hushhq/hush-server/internal/auth"
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

func TestLiveKitVoiceState_ReturnsCurrentVoiceParticipantsForServer(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	voiceA := uuid.New().String()
	voiceB := uuid.New().String()
	textID := uuid.New().String()
	state := NewVoiceState()
	state.join("channel-"+voiceA, "user-alice", "Alice")
	state.join("channel-"+voiceA, "user-bob", "Bob")
	state.join("channel-"+voiceB, "user-caro", "Caro")
	state.join("channel-"+textID, "user-text", "Text")

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, sid, uid string) (int, error) {
			if sid == serverID && uid == userID {
				return models.PermissionLevelMember, nil
			}
			return 0, errNotFoundLikeMember
		},
		listChannelsFn: func(_ context.Context, sid string) ([]models.Channel, error) {
			require.Equal(t, serverID, sid)
			return []models.Channel{
				{ID: voiceA, Type: "voice"},
				{ID: voiceB, Type: "voice"},
				{ID: textID, Type: "text"},
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := LiveKitRoutesWithVoiceState(store, testJWTSecret, "test-key", "test-secret", state)

	req := httptest.NewRequest(http.MethodGet, "/voice-state?serverId="+serverID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body struct {
		ParticipantsByChannel map[string][]voiceParticipant `json:"participantsByChannel"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	require.Len(t, body.ParticipantsByChannel[voiceA], 2)
	require.Len(t, body.ParticipantsByChannel[voiceB], 1)
	assert.NotContains(t, body.ParticipantsByChannel, textID)
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
					ID: channelID,
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
					ID: channelID,
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

func TestLiveKitToken_ReturnsPublicSignalingURL_WhenConfigured(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	router := LiveKitRoutesWithVoiceStateAndPublicURL(
		store,
		testJWTSecret,
		"test-key",
		"test-secret",
		"wss://rtc.example.com/",
		nil,
	)

	rr := postLiveKitToken(router, "custom-room", "Alice", token)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.NotEmpty(t, body["token"])
	assert.Equal(t, "wss://rtc.example.com/", body["livekitUrl"])
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

// ---------- Device-scoped MLS metadata on LiveKit tokens ----------

// decodeLivekitJWTClaims base64-decodes the payload segment of a JWT
// and returns it as a generic map. We avoid going through the LiveKit
// verifier so the test pins the claim shape directly rather than the
// verifier's internal grant struct.
func decodeLivekitJWTClaims(t *testing.T, token string) map[string]interface{} {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT must have 3 segments")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

// signAuthJWTWithDevice mints a Hush auth JWT with the supplied
// deviceID and registers the matching session on the mock store. It
// lets tests exercise the LiveKit token handler with a chosen
// deviceID (or with an empty one to prove the device-id gate).
func signAuthJWTWithDevice(store *mockStore, userID, deviceID string) string {
	sessionID := uuid.New().String()
	token, err := internalauth.SignJWT(userID, sessionID, deviceID, testJWTSecret, time.Now().Add(time.Hour))
	if err != nil {
		panic(err)
	}
	hash := internalauth.TokenHash(token)
	store.getSessionByTokenHashFn = func(_ context.Context, th string) (*models.Session, error) {
		if th != hash {
			return nil, nil
		}
		return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	return token
}

func TestLiveKitToken_EmbedsDeviceScopedMlsMetadata(t *testing.T) {
	// Voice-MLS eviction depends on the participant metadata claim
	// carrying `userId`, `deviceId`, and a `mlsIdentity` of
	// `userId:deviceId`. Remote peers parse that to call
	// `removeMembers` against the exact MLS leaf when this
	// participant disconnects. Bare LiveKit `participant.identity`
	// (which stays as the user id for moderation) is not sufficient.
	userID := uuid.New().String()
	store := &mockStore{}
	jwt := signAuthJWTWithDevice(store, userID, "device-abc")

	rr := postLiveKitToken(livekitRouter(store), "custom-room", "Alice", jwt)
	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	require.NotEmpty(t, body["token"])

	claims := decodeLivekitJWTClaims(t, body["token"])
	rawMetadata, _ := claims["metadata"].(string)
	require.NotEmpty(t, rawMetadata, "LiveKit token must carry participant metadata")

	var md map[string]string
	require.NoError(t, json.Unmarshal([]byte(rawMetadata), &md))
	assert.Equal(t, userID, md["userId"])
	assert.Equal(t, "device-abc", md["deviceId"])
	assert.Equal(t, userID+":device-abc", md["mlsIdentity"])

	// Moderation regression: LiveKit `identity` (the JWT `sub`) MUST
	// stay as the bare user id so RemoveParticipant(room, userID) and
	// voice-state snapshots keep working unchanged.
	assert.Equal(t, userID, claims["sub"])
}

func TestLiveKitToken_MissingDeviceID_Returns403AndDoesNotMintToken(t *testing.T) {
	// A session JWT without a device id reaches the handler when an
	// older client or a guest session hits /api/livekit/token. The
	// handler must fail closed with a clear code, never mint a token,
	// because the remote-eviction path has no usable MLS identity for
	// such a participant.
	userID := uuid.New().String()
	store := &mockStore{}
	jwt := signAuthJWTWithDevice(store, userID, "")

	rr := postLiveKitToken(livekitRouter(store), "custom-room", "Alice", jwt)

	require.Equal(t, http.StatusForbidden, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "missing_device_id", body["code"])
	assert.Empty(t, body["token"], "no token may be issued when device id is missing")
}
