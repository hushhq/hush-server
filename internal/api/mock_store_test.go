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

	createServerWithOwnerFn   func(ctx context.Context, name string, iconURL *string, ownerID string) (*models.Server, error)
	getServerByIDFn           func(ctx context.Context, serverID string) (*models.Server, error)
	listServersForUserFn      func(ctx context.Context, userID string) ([]models.ServerWithRole, error)
	updateServerFn            func(ctx context.Context, serverID string, name *string, iconURL *string) error
	deleteServerFn            func(ctx context.Context, serverID string) error
	addServerMemberFn         func(ctx context.Context, serverID, userID, role string) error
	removeServerMemberFn      func(ctx context.Context, serverID, userID string) error
	getServerMemberFn         func(ctx context.Context, serverID, userID string) (*models.ServerMember, error)
	listServerMembersFn       func(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error)
	transferServerOwnershipFn func(ctx context.Context, serverID, newOwnerID string) error
	updateServerMemberRoleFn  func(ctx context.Context, serverID, userID, role string) error
	countServerMembersFn      func(ctx context.Context, serverID string) (int, error)
	getNextOwnerCandidateFn   func(ctx context.Context, serverID, excludeUserID string) (*models.ServerMember, error)

	createChannelFn         func(ctx context.Context, serverID, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	listChannelsFn          func(ctx context.Context, serverID string) ([]models.Channel, error)
	getChannelByIDFn        func(ctx context.Context, channelID string) (*models.Channel, error)
	deleteChannelFn         func(ctx context.Context, channelID string) error
	moveChannelFn           func(ctx context.Context, channelID string, parentID *string, position int) error
	getServerIDForChannelFn func(ctx context.Context, channelID string) (string, error)

	createInviteFn    func(ctx context.Context, code, serverID, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	getInviteByCodeFn func(ctx context.Context, code string) (*models.InviteCode, error)
	claimInviteUseFn  func(ctx context.Context, code string) (bool, error)
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

func (m *mockStore) CreateServerWithOwner(ctx context.Context, name string, iconURL *string, ownerID string) (*models.Server, error) {
	if m.createServerWithOwnerFn != nil {
		return m.createServerWithOwnerFn(ctx, name, iconURL, ownerID)
	}
	return nil, nil
}

func (m *mockStore) GetServerByID(ctx context.Context, serverID string) (*models.Server, error) {
	if m.getServerByIDFn != nil {
		return m.getServerByIDFn(ctx, serverID)
	}
	return nil, nil
}

func (m *mockStore) ListServersForUser(ctx context.Context, userID string) ([]models.ServerWithRole, error) {
	if m.listServersForUserFn != nil {
		return m.listServersForUserFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockStore) UpdateServer(ctx context.Context, serverID string, name *string, iconURL *string) error {
	if m.updateServerFn != nil {
		return m.updateServerFn(ctx, serverID, name, iconURL)
	}
	return nil
}

func (m *mockStore) DeleteServer(ctx context.Context, serverID string) error {
	if m.deleteServerFn != nil {
		return m.deleteServerFn(ctx, serverID)
	}
	return nil
}

func (m *mockStore) AddServerMember(ctx context.Context, serverID, userID, role string) error {
	if m.addServerMemberFn != nil {
		return m.addServerMemberFn(ctx, serverID, userID, role)
	}
	return nil
}

func (m *mockStore) RemoveServerMember(ctx context.Context, serverID, userID string) error {
	if m.removeServerMemberFn != nil {
		return m.removeServerMemberFn(ctx, serverID, userID)
	}
	return nil
}

func (m *mockStore) GetServerMember(ctx context.Context, serverID, userID string) (*models.ServerMember, error) {
	if m.getServerMemberFn != nil {
		return m.getServerMemberFn(ctx, serverID, userID)
	}
	return nil, nil
}

func (m *mockStore) ListServerMembers(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error) {
	if m.listServerMembersFn != nil {
		return m.listServerMembersFn(ctx, serverID)
	}
	return nil, nil
}

func (m *mockStore) TransferServerOwnership(ctx context.Context, serverID, newOwnerID string) error {
	if m.transferServerOwnershipFn != nil {
		return m.transferServerOwnershipFn(ctx, serverID, newOwnerID)
	}
	return nil
}

func (m *mockStore) UpdateServerMemberRole(ctx context.Context, serverID, userID, role string) error {
	if m.updateServerMemberRoleFn != nil {
		return m.updateServerMemberRoleFn(ctx, serverID, userID, role)
	}
	return nil
}

func (m *mockStore) CountServerMembers(ctx context.Context, serverID string) (int, error) {
	if m.countServerMembersFn != nil {
		return m.countServerMembersFn(ctx, serverID)
	}
	return 0, nil
}

func (m *mockStore) GetNextOwnerCandidate(ctx context.Context, serverID, excludeUserID string) (*models.ServerMember, error) {
	if m.getNextOwnerCandidateFn != nil {
		return m.getNextOwnerCandidateFn(ctx, serverID, excludeUserID)
	}
	return nil, nil
}

func (m *mockStore) CreateChannel(ctx context.Context, serverID, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error) {
	if m.createChannelFn != nil {
		return m.createChannelFn(ctx, serverID, name, channelType, voiceMode, parentID, position)
	}
	return nil, nil
}

func (m *mockStore) ListChannels(ctx context.Context, serverID string) ([]models.Channel, error) {
	if m.listChannelsFn != nil {
		return m.listChannelsFn(ctx, serverID)
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

func (m *mockStore) GetServerIDForChannel(ctx context.Context, channelID string) (string, error) {
	if m.getServerIDForChannelFn != nil {
		return m.getServerIDForChannelFn(ctx, channelID)
	}
	return "", nil
}

func (m *mockStore) CreateInvite(ctx context.Context, code, serverID, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error) {
	if m.createInviteFn != nil {
		return m.createInviteFn(ctx, code, serverID, createdBy, maxUses, expiresAt)
	}
	return &models.InviteCode{Code: code, ServerID: serverID, CreatedBy: createdBy, MaxUses: maxUses, ExpiresAt: expiresAt}, nil
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
