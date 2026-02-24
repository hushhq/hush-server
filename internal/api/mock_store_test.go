package api

import (
	"context"
	"time"

	"hush.app/server/internal/models"
)

// mockStore implements db.Store with function fields for per-test customization.
type mockStore struct {
	createUserFn                  func(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error)
	getUserByUsernameFn           func(ctx context.Context, username string) (*models.User, error)
	getUserByIDFn                 func(ctx context.Context, id string) (*models.User, error)
	createSessionFn               func(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	getSessionByTokenHashFn       func(ctx context.Context, tokenHash string) (*models.Session, error)
	deleteSessionByIDFn           func(ctx context.Context, sessionID string) error
	upsertIdentityKeysFn          func(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error
	insertOneTimePreKeysFn        func(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error
	getIdentityAndSignedPreKeyFn  func(ctx context.Context, userID, deviceID string) (identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int, err error)
	consumeOneTimePreKeyFn        func(ctx context.Context, userID, deviceID string) (keyID int, publicKey []byte, err error)
	countUnusedOneTimePreKeysFn   func(ctx context.Context, userID, deviceID string) (int, error)
	listDeviceIDsForUserFn        func(ctx context.Context, userID string) ([]string, error)
	upsertDeviceFn                func(ctx context.Context, userID, deviceID, label string) error
	insertMessageFn               func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	getMessagesFn                 func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn             func(ctx context.Context, channelID, userID string) (bool, error)
}

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
