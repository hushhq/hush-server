package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serversRouter(store *mockStore) http.Handler {
	return ServerRoutes(store, testJWTSecret)
}

// makeServerAuth sets getSessionByTokenHashFn on store so token is valid; returns token and userID.
func makeServerAuth(store *mockStore, userID string) string {
	sessionID := uuid.New().String()
	token, err := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	if err != nil {
		panic(err)
	}
	tokenHash := auth.TokenHash(token)
	store.getSessionByTokenHashFn = func(_ context.Context, th string) (*models.Session, error) {
		if th != tokenHash {
			return nil, nil
		}
		return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	return token
}

func postServerJSON(handler http.Handler, path string, body interface{}, token string) *httptest.ResponseRecorder {
	var bodyReader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getServer(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func putServerJSON(handler http.Handler, path string, body interface{}, token string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func deleteServer(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeServer(t *testing.T, rr *httptest.ResponseRecorder) models.Server {
	t.Helper()
	var s models.Server
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&s))
	return s
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var m map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&m))
	return m
}

func TestCreateServer_ValidInput_ReturnsServer(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	serverID := uuid.New().String()
	store.createServerWithOwnerFn = func(_ context.Context, name string, _ *string, ownerID string) (*models.Server, error) {
		if ownerID != userID {
			return nil, assert.AnError
		}
		return &models.Server{ID: serverID, Name: name, OwnerID: ownerID, CreatedAt: time.Now()}, nil
	}
	router := serversRouter(store)
	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: "My Server"}, token)
	assert.Equal(t, http.StatusCreated, rr.Code)
	s := decodeServer(t, rr)
	assert.Equal(t, "My Server", s.Name)
	assert.Equal(t, userID, s.OwnerID)
	assert.Equal(t, serverID, s.ID)
}

func TestCreateServer_MissingName_Returns400(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	router := serversRouter(store)
	rr := postServerJSON(router, "/", models.CreateServerRequest{Name: ""}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "name is required")
}

func TestListServers_ReturnsUserServers(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.listServersForUserFn = func(_ context.Context, uid string) ([]models.ServerWithRole, error) {
		if uid != userID {
			return nil, nil
		}
		return []models.ServerWithRole{
			{Server: models.Server{ID: "s1", Name: "S1", OwnerID: userID}, Role: "admin"},
		}, nil
	}
	router := serversRouter(store)
	rr := getServer(router, "/", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var list []models.ServerWithRole
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list, 1)
	assert.Equal(t, "S1", list[0].Name)
	assert.Equal(t, "admin", list[0].Role)
}

func TestGetServer_NotMember_Returns403(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, _, _ string) (*models.ServerMember, error) {
		return nil, nil
	}
	router := serversRouter(store)
	rr := getServer(router, "/"+serverID, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "not a member")
}

func TestGetServer_AsMember_ReturnsServerWithChannels(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID, Role: "member"}, nil
		}
		return nil, nil
	}
	store.getServerByIDFn = func(_ context.Context, sid string) (*models.Server, error) {
		if sid == serverID {
			return &models.Server{ID: serverID, Name: "S1", OwnerID: userID}, nil
		}
		return nil, nil
	}
	store.listChannelsFn = func(_ context.Context, sid string) ([]models.Channel, error) {
		if sid == serverID {
			return []models.Channel{{ID: "ch1", ServerID: serverID, Name: "general", Type: "text", Position: 0}}, nil
		}
		return nil, nil
	}
	router := serversRouter(store)
	rr := getServer(router, "/"+serverID, token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var out serverWithChannelsResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	assert.Equal(t, "S1", out.Server.Name)
	require.Len(t, out.Channels, 1)
	assert.Equal(t, "general", out.Channels[0].Name)
}

func TestUpdateServer_NotAdmin_Returns403(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID, Role: "member"}, nil
		}
		return nil, nil
	}
	router := serversRouter(store)
	rr := putServerJSON(router, "/"+serverID, models.UpdateServerRequest{Name: ptrString("New")}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "admin")
}

func TestUpdateServer_AsAdmin_Succeeds(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID, Role: "admin"}, nil
		}
		return nil, nil
	}
	store.updateServerFn = func(_ context.Context, sid string, name *string, _ *string) error {
		assert.Equal(t, serverID, sid)
		require.NotNil(t, name)
		assert.Equal(t, "New Name", *name)
		return nil
	}
	router := serversRouter(store)
	rr := putServerJSON(router, "/"+serverID, models.UpdateServerRequest{Name: ptrString("New Name")}, token)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestDeleteServer_NotAdmin_Returns403(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID, Role: "member"}, nil
		}
		return nil, nil
	}
	router := serversRouter(store)
	rr := deleteServer(router, "/"+serverID, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestDeleteServer_AsAdmin_Returns204(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID, Role: "admin"}, nil
		}
		return nil, nil
	}
	store.deleteServerFn = func(_ context.Context, sid string) error {
		assert.Equal(t, serverID, sid)
		return nil
	}
	router := serversRouter(store)
	rr := deleteServer(router, "/"+serverID, token)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestJoinServer_ValidInvite_AddsMember(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	code := "ABC123"
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getInviteByCodeFn = func(_ context.Context, c string) (*models.InviteCode, error) {
		if c != code {
			return nil, nil
		}
		return &models.InviteCode{Code: code, ServerID: serverID}, nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		return nil, nil
	}
	store.claimInviteUseFn = func(_ context.Context, c string) (bool, error) {
		assert.Equal(t, code, c)
		return true, nil
	}
	store.addServerMemberFn = func(_ context.Context, sid, uid, role string) error {
		assert.Equal(t, serverID, sid)
		assert.Equal(t, userID, uid)
		assert.Equal(t, "member", role)
		return nil
	}
	store.getServerByIDFn = func(_ context.Context, sid string) (*models.Server, error) {
		if sid == serverID {
			return &models.Server{ID: serverID, Name: "S1"}, nil
		}
		return nil, nil
	}
	router := serversRouter(store)
	rr := postServerJSON(router, "/"+serverID+"/join", models.JoinServerRequest{InviteCode: code}, token)
	assert.Equal(t, http.StatusOK, rr.Code)
	s := decodeServer(t, rr)
	assert.Equal(t, serverID, s.ID)
}

func TestJoinServer_ExpiredOrExhaustedInvite_Returns400(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return &models.InviteCode{Code: "x", ServerID: serverID}, nil
	}
	store.getServerMemberFn = func(_ context.Context, _, _ string) (*models.ServerMember, error) {
		return nil, nil
	}
	store.claimInviteUseFn = func(_ context.Context, _ string) (bool, error) {
		return false, nil
	}
	router := serversRouter(store)
	rr := postServerJSON(router, "/"+serverID+"/join", models.JoinServerRequest{InviteCode: "x"}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "expired or reached maximum")
}

func TestJoinServer_AlreadyMember_Returns409(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getInviteByCodeFn = func(_ context.Context, _ string) (*models.InviteCode, error) {
		return &models.InviteCode{Code: "x", ServerID: serverID}, nil
	}
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{}, nil
		}
		return nil, nil
	}
	router := serversRouter(store)
	rr := postServerJSON(router, "/"+serverID+"/join", models.JoinServerRequest{InviteCode: "x"}, token)
	assert.Equal(t, http.StatusConflict, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "already a member")
}

func TestLeaveServer_TransfersOwnership(t *testing.T) {
	userID := uuid.New().String()
	otherID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid != serverID {
			return nil, nil
		}
		if uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID, Role: "admin"}, nil
		}
		if uid == otherID {
			return &models.ServerMember{ServerID: serverID, UserID: otherID, Role: "member"}, nil
		}
		return nil, nil
	}
	store.getServerByIDFn = func(_ context.Context, sid string) (*models.Server, error) {
		if sid == serverID {
			return &models.Server{ID: serverID, OwnerID: userID}, nil
		}
		return nil, nil
	}
	store.countServerMembersFn = func(_ context.Context, sid string) (int, error) {
		if sid == serverID {
			return 2, nil
		}
		return 0, nil
	}
	store.getNextOwnerCandidateFn = func(_ context.Context, sid, exclude string) (*models.ServerMember, error) {
		if sid == serverID && exclude == userID {
			return &models.ServerMember{ServerID: serverID, UserID: otherID, Role: "member"}, nil
		}
		return nil, nil
	}
	var transferred, roleUpdated bool
	store.transferServerOwnershipFn = func(_ context.Context, sid, newOwner string) error {
		assert.Equal(t, serverID, sid)
		assert.Equal(t, otherID, newOwner)
		transferred = true
		return nil
	}
	store.updateServerMemberRoleFn = func(_ context.Context, sid, uid, role string) error {
		roleUpdated = true
		return nil
	}
	store.removeServerMemberFn = func(_ context.Context, sid, uid string) error {
		assert.Equal(t, serverID, sid)
		assert.Equal(t, userID, uid)
		return nil
	}
	router := serversRouter(store)
	rr := postServerJSON(router, "/"+serverID+"/leave", nil, token)
	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, transferred)
	assert.True(t, roleUpdated)
}

func TestLeaveServer_SoleMember_Returns409(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	store := &mockStore{}
	token := makeServerAuth(store, userID)
	store.getServerMemberFn = func(_ context.Context, sid, uid string) (*models.ServerMember, error) {
		if sid == serverID && uid == userID {
			return &models.ServerMember{ServerID: serverID, UserID: userID}, nil
		}
		return nil, nil
	}
	store.countServerMembersFn = func(_ context.Context, sid string) (int, error) {
		if sid == serverID {
			return 1, nil
		}
		return 0, nil
	}
	router := serversRouter(store)
	rr := postServerJSON(router, "/"+serverID+"/leave", nil, token)
	assert.Equal(t, http.StatusConflict, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "sole member")
}

func ptrString(s string) *string {
	return &s
}
