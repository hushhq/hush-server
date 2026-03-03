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
	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// compile-time interface check: mockStore must satisfy db.Store.
var _ db.Store = (*mockStore)(nil)

// mockStore implements db.Store with function fields for per-test customization.
// Unset fields return sensible zero values so tests only set what they care about.
type mockStore struct {
	// User/session
	createUserFn            func(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error)
	getUserByUsernameFn     func(ctx context.Context, username string) (*models.User, error)
	getUserByIDFn           func(ctx context.Context, id string) (*models.User, error)
	createSessionFn         func(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	getSessionByTokenHashFn func(ctx context.Context, tokenHash string) (*models.Session, error)
	deleteSessionByIDFn     func(ctx context.Context, sessionID string) error

	// Signal keys
	upsertIdentityKeysFn         func(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error
	insertOneTimePreKeysFn        func(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error
	getIdentityAndSignedPreKeyFn  func(ctx context.Context, userID, deviceID string) (identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int, err error)
	consumeOneTimePreKeyFn        func(ctx context.Context, userID, deviceID string) (keyID int, publicKey []byte, err error)
	countUnusedOneTimePreKeysFn   func(ctx context.Context, userID, deviceID string) (int, error)
	listDeviceIDsForUserFn        func(ctx context.Context, userID string) ([]string, error)
	upsertDeviceFn                func(ctx context.Context, userID, deviceID, label string) error

	// Messages
	insertMessageFn   func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	getMessagesFn     func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn func(ctx context.Context, channelID, userID string) (bool, error)

	// Instance
	getInstanceConfigFn    func(ctx context.Context) (*models.InstanceConfig, error)
	updateInstanceConfigFn func(ctx context.Context, name *string, iconURL *string, registrationMode *string) error
	setInstanceOwnerFn     func(ctx context.Context, userID string) (bool, error)
	getUserRoleFn          func(ctx context.Context, userID string) (string, error)
	updateUserRoleFn       func(ctx context.Context, userID, role string) error
	listMembersFn          func(ctx context.Context) ([]models.Member, error)

	// Channels (no serverID)
	createChannelFn  func(ctx context.Context, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	listChannelsFn   func(ctx context.Context) ([]models.Channel, error)
	getChannelByIDFn func(ctx context.Context, channelID string) (*models.Channel, error)
	deleteChannelFn  func(ctx context.Context, channelID string) error
	moveChannelFn    func(ctx context.Context, channelID string, parentID *string, position int) error

	// Invites (no serverID)
	createInviteFn    func(ctx context.Context, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	getInviteByCodeFn func(ctx context.Context, code string) (*models.InviteCode, error)
	claimInviteUseFn  func(ctx context.Context, code string) (bool, error)
}

// ---------- User/session ----------

func (m *mockStore) CreateUser(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error) {
	if m.createUserFn != nil {
		return m.createUserFn(ctx, username, displayName, passwordHash)
	}
	return nil, nil
}

func (m *mockStore) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	if m.getUserByUsernameFn != nil {
		return m.getUserByUsernameFn(ctx, username)
	}
	return nil, nil
}

func (m *mockStore) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	if m.getUserByIDFn != nil {
		return m.getUserByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *mockStore) CreateSession(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error) {
	if m.createSessionFn != nil {
		return m.createSessionFn(ctx, sessionID, userID, tokenHash, expiresAt)
	}
	return &models.Session{
		ID:        sessionID,
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}, nil
}

func (m *mockStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*models.Session, error) {
	if m.getSessionByTokenHashFn != nil {
		return m.getSessionByTokenHashFn(ctx, tokenHash)
	}
	return nil, nil
}

func (m *mockStore) DeleteSessionByID(ctx context.Context, sessionID string) error {
	if m.deleteSessionByIDFn != nil {
		return m.deleteSessionByIDFn(ctx, sessionID)
	}
	return nil
}

// ---------- Signal keys ----------

func (m *mockStore) UpsertIdentityKeys(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error {
	if m.upsertIdentityKeysFn != nil {
		return m.upsertIdentityKeysFn(ctx, userID, deviceID, identityKey, signedPreKey, signedPreKeySignature, registrationID)
	}
	return nil
}

func (m *mockStore) InsertOneTimePreKeys(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error {
	if m.insertOneTimePreKeysFn != nil {
		return m.insertOneTimePreKeysFn(ctx, userID, deviceID, keys)
	}
	return nil
}

func (m *mockStore) GetIdentityAndSignedPreKey(ctx context.Context, userID, deviceID string) ([]byte, []byte, []byte, int, error) {
	if m.getIdentityAndSignedPreKeyFn != nil {
		return m.getIdentityAndSignedPreKeyFn(ctx, userID, deviceID)
	}
	return nil, nil, nil, 0, nil
}

func (m *mockStore) ConsumeOneTimePreKey(ctx context.Context, userID, deviceID string) (int, []byte, error) {
	if m.consumeOneTimePreKeyFn != nil {
		return m.consumeOneTimePreKeyFn(ctx, userID, deviceID)
	}
	return 0, nil, nil
}

func (m *mockStore) CountUnusedOneTimePreKeys(ctx context.Context, userID, deviceID string) (int, error) {
	if m.countUnusedOneTimePreKeysFn != nil {
		return m.countUnusedOneTimePreKeysFn(ctx, userID, deviceID)
	}
	return 0, nil
}

func (m *mockStore) ListDeviceIDsForUser(ctx context.Context, userID string) ([]string, error) {
	if m.listDeviceIDsForUserFn != nil {
		return m.listDeviceIDsForUserFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockStore) UpsertDevice(ctx context.Context, userID, deviceID, label string) error {
	if m.upsertDeviceFn != nil {
		return m.upsertDeviceFn(ctx, userID, deviceID, label)
	}
	return nil
}

// ---------- Messages ----------

func (m *mockStore) InsertMessage(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error) {
	if m.insertMessageFn != nil {
		return m.insertMessageFn(ctx, channelID, senderID, recipientID, ciphertext)
	}
	return nil, nil
}

func (m *mockStore) GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error) {
	if m.getMessagesFn != nil {
		return m.getMessagesFn(ctx, channelID, recipientID, before, limit)
	}
	return nil, nil
}

func (m *mockStore) IsChannelMember(ctx context.Context, channelID, userID string) (bool, error) {
	if m.isChannelMemberFn != nil {
		return m.isChannelMemberFn(ctx, channelID, userID)
	}
	return false, nil
}

// ---------- Instance ----------

func (m *mockStore) GetInstanceConfig(ctx context.Context) (*models.InstanceConfig, error) {
	if m.getInstanceConfigFn != nil {
		return m.getInstanceConfigFn(ctx)
	}
	return &models.InstanceConfig{
		ID:               "inst-1",
		Name:             "Test Instance",
		RegistrationMode: "open",
	}, nil
}

func (m *mockStore) UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string) error {
	if m.updateInstanceConfigFn != nil {
		return m.updateInstanceConfigFn(ctx, name, iconURL, registrationMode)
	}
	return nil
}

func (m *mockStore) SetInstanceOwner(ctx context.Context, userID string) (bool, error) {
	if m.setInstanceOwnerFn != nil {
		return m.setInstanceOwnerFn(ctx, userID)
	}
	// Default: not first user (owner already set).
	return false, nil
}

func (m *mockStore) GetUserRole(ctx context.Context, userID string) (string, error) {
	if m.getUserRoleFn != nil {
		return m.getUserRoleFn(ctx, userID)
	}
	return "member", nil
}

func (m *mockStore) UpdateUserRole(ctx context.Context, userID, role string) error {
	if m.updateUserRoleFn != nil {
		return m.updateUserRoleFn(ctx, userID, role)
	}
	return nil
}

func (m *mockStore) ListMembers(ctx context.Context) ([]models.Member, error) {
	if m.listMembersFn != nil {
		return m.listMembersFn(ctx)
	}
	return nil, nil
}

// ---------- Channels ----------

func (m *mockStore) CreateChannel(ctx context.Context, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error) {
	if m.createChannelFn != nil {
		return m.createChannelFn(ctx, name, channelType, voiceMode, parentID, position)
	}
	return nil, nil
}

func (m *mockStore) ListChannels(ctx context.Context) ([]models.Channel, error) {
	if m.listChannelsFn != nil {
		return m.listChannelsFn(ctx)
	}
	return nil, nil
}

func (m *mockStore) GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error) {
	if m.getChannelByIDFn != nil {
		return m.getChannelByIDFn(ctx, channelID)
	}
	return nil, nil
}

func (m *mockStore) DeleteChannel(ctx context.Context, channelID string) error {
	if m.deleteChannelFn != nil {
		return m.deleteChannelFn(ctx, channelID)
	}
	return nil
}

func (m *mockStore) MoveChannel(ctx context.Context, channelID string, parentID *string, position int) error {
	if m.moveChannelFn != nil {
		return m.moveChannelFn(ctx, channelID, parentID, position)
	}
	return nil
}

// ---------- Invites ----------

func (m *mockStore) CreateInvite(ctx context.Context, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error) {
	if m.createInviteFn != nil {
		return m.createInviteFn(ctx, code, createdBy, maxUses, expiresAt)
	}
	return &models.InviteCode{Code: code, CreatedBy: createdBy, MaxUses: maxUses, ExpiresAt: expiresAt}, nil
}

func (m *mockStore) GetInviteByCode(ctx context.Context, code string) (*models.InviteCode, error) {
	if m.getInviteByCodeFn != nil {
		return m.getInviteByCodeFn(ctx, code)
	}
	return nil, nil
}

func (m *mockStore) ClaimInviteUse(ctx context.Context, code string) (bool, error) {
	if m.claimInviteUseFn != nil {
		return m.claimInviteUseFn(ctx, code)
	}
	return true, nil
}

// ---------- Shared test helpers ----------

// makeAuth creates a valid JWT and wires getSessionByTokenHashFn on the store.
// Returns the bearer token string.
func makeAuth(store *mockStore, userID string) string {
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

// makeServerAuth is an alias for makeAuth, kept for backward compatibility with
// tests that were written when a server-scoped auth helper was needed.
func makeServerAuth(store *mockStore, userID string) string {
	return makeAuth(store, userID)
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

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var m map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&m))
	return m
}

func ptrString(s string) *string {
	return &s
}

func ptrInt(n int) *int {
	return &n
}
