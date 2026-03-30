package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	pingFn func(ctx context.Context) error

	// User/session — BIP39 public-key identity
	createUserWithPublicKeyFn func(ctx context.Context, username, displayName string, publicKey []byte) (*models.User, error)
	getUserByPublicKeyFn      func(ctx context.Context, publicKey []byte) (*models.User, error)
	getUserByUsernameFn       func(ctx context.Context, username string) (*models.User, error)
	getUserByIDFn             func(ctx context.Context, id string) (*models.User, error)

	// BIP39 auth nonces
	insertAuthNonceFn    func(ctx context.Context, nonce string, publicKey []byte, expiresAt time.Time) error
	consumeAuthNonceFn   func(ctx context.Context, nonce string) ([]byte, error)
	deleteAuthNonceFn    func(ctx context.Context, nonce string) error
	purgeExpiredNoncesFn func(ctx context.Context) (int64, error)

	// Device keys
	insertDeviceKeyFn       func(ctx context.Context, userID, deviceID, label string, devicePublicKey, certificate []byte) error
	listDeviceKeysFn        func(ctx context.Context, userID string) ([]models.DeviceKey, error)
	revokeDeviceKeyFn       func(ctx context.Context, userID, deviceID string) error
	revokeAllDeviceKeysFn   func(ctx context.Context, userID string) error
	updateDeviceLastSeenFn  func(ctx context.Context, userID, deviceID string) error
	createSessionFn         func(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	getSessionByTokenHashFn func(ctx context.Context, tokenHash string) (*models.Session, error)
	deleteSessionByIDFn     func(ctx context.Context, sessionID string) error

	// MLS credentials
	upsertMLSCredentialFn func(ctx context.Context, userID, deviceID string, credentialBytes, signingPublicKey []byte, identityVersion int) error
	getMLSCredentialFn    func(ctx context.Context, userID, deviceID string) (credentialBytes, signingPublicKey []byte, identityVersion int, err error)

	// MLS key packages
	insertMLSKeyPackagesFn          func(ctx context.Context, userID, deviceID string, packages [][]byte, expiresAt time.Time) error
	insertMLSLastResortKeyPackageFn func(ctx context.Context, userID, deviceID string, keyPackageBytes []byte) error
	consumeMLSKeyPackageFn          func(ctx context.Context, userID, deviceID string) (keyPackageBytes []byte, err error)
	countUnusedMLSKeyPackagesFn     func(ctx context.Context, userID, deviceID string) (int, error)
	purgeExpiredMLSKeyPackagesFn    func(ctx context.Context) (int64, error)

	// Device enumeration
	listDeviceIDsForUserFn func(ctx context.Context, userID string) ([]string, error)
	upsertDeviceFn         func(ctx context.Context, userID, deviceID, label string) error

	// Messages
	insertMessageFn   func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	getMessagesFn     func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn func(ctx context.Context, channelID, userID string) (bool, error)

	// Instance
	getInstanceConfigFn    func(ctx context.Context) (*models.InstanceConfig, error)
	updateInstanceConfigFn func(ctx context.Context, name *string, iconURL *string, registrationMode *string, guildDiscovery *string, serverCreationPolicy *string) error
	getUserRoleFn          func(ctx context.Context, userID string) (string, error)
	updateUserRoleFn       func(ctx context.Context, userID, role string) error
	listMembersFn          func(ctx context.Context) ([]models.Member, error)

	// Channels (guild-scoped — serverID param)
	createChannelFn               func(ctx context.Context, serverID string, encryptedMetadata []byte, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	listChannelsFn                func(ctx context.Context, serverID string) ([]models.Channel, error)
	getChannelByIDFn              func(ctx context.Context, channelID string) (*models.Channel, error)
	getChannelByTypeAndPositionFn func(ctx context.Context, serverID, channelType string, position int) (*models.Channel, error)
	deleteChannelFn               func(ctx context.Context, channelID string) error
	moveChannelFn                 func(ctx context.Context, channelID string, parentID *string, position int) error

	// Server templates
	listServerTemplatesFn      func(ctx context.Context) ([]models.ServerTemplate, error)
	getServerTemplateByIDFn    func(ctx context.Context, id string) (*models.ServerTemplate, error)
	getDefaultServerTemplateFn func(ctx context.Context) (*models.ServerTemplate, error)
	createServerTemplateFn     func(ctx context.Context, name string, channels json.RawMessage, isDefault bool) (*models.ServerTemplate, error)
	updateServerTemplateFn     func(ctx context.Context, id string, name string, channels json.RawMessage, isDefault bool) error
	deleteServerTemplateFn     func(ctx context.Context, id string) error

	// Invites (guild-scoped — serverID param)
	createInviteFn    func(ctx context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	getInviteByCodeFn func(ctx context.Context, code string) (*models.InviteCode, error)
	claimInviteUseFn  func(ctx context.Context, code string) (bool, error)

	// Server / guild operations
	createServerFn                  func(ctx context.Context, encryptedMetadata []byte) (*models.Server, error)
	updateServerEncryptedMetadataFn func(ctx context.Context, serverID string, encryptedMetadata []byte) error
	getServerByIDFn                 func(ctx context.Context, serverID string) (*models.Server, error)
	listServersForUserFn            func(ctx context.Context, userID string) ([]models.Server, error)
	deleteServerFn                  func(ctx context.Context, serverID string) error
	listGuildBillingStatsFn         func(ctx context.Context) ([]models.GuildBillingStats, error)

	// Server member operations
	addServerMemberFn         func(ctx context.Context, serverID, userID string, permissionLevel int) error
	removeServerMemberFn      func(ctx context.Context, serverID, userID string) error
	getServerMemberLevelFn    func(ctx context.Context, serverID, userID string) (int, error)
	updateServerMemberLevelFn func(ctx context.Context, serverID, userID string, permissionLevel int) error
	listServerMembersFn       func(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error)

	// Moderation — bans (guild-scoped — serverID param)
	insertBanFn      func(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Ban, error)
	getActiveBanFn   func(ctx context.Context, serverID, userID string) (*models.Ban, error)
	liftBanFn        func(ctx context.Context, banID, liftedByID string) error
	listActiveBansFn func(ctx context.Context, serverID string) ([]models.Ban, error)

	// Moderation — mutes (guild-scoped — serverID param)
	insertMuteFn      func(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Mute, error)
	getActiveMuteFn   func(ctx context.Context, serverID, userID string) (*models.Mute, error)
	liftMuteFn        func(ctx context.Context, muteID, liftedByID string) error
	listActiveMutesFn func(ctx context.Context, serverID string) ([]models.Mute, error)

	// Moderation — audit log (guild-scoped — serverID param)
	insertAuditLogFn func(ctx context.Context, serverID, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error
	listAuditLogFn   func(ctx context.Context, serverID string, limit, offset int, filter *db.AuditLogFilter) ([]models.AuditLogEntry, error)

	// Moderation — messages
	getMessageByIDFn func(ctx context.Context, messageID string) (*models.Message, error)
	deleteMessageFn  func(ctx context.Context, messageID string) error

	// Moderation — sessions
	deleteSessionsByUserIDFn func(ctx context.Context, userID string) error

	// Instance bans
	insertInstanceBanFn    func(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.InstanceBan, error)
	getActiveInstanceBanFn func(ctx context.Context, userID string) (*models.InstanceBan, error)
	liftInstanceBanFn      func(ctx context.Context, banID, liftedByID string) error

	// Instance audit log
	insertInstanceAuditLogFn func(ctx context.Context, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error
	listInstanceAuditLogFn   func(ctx context.Context, limit, offset int, filter *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error)

	// User search
	searchUsersFn func(ctx context.Context, query string, limit int) ([]models.UserSearchResult, error)

	// System messages
	insertSystemMessageFn       func(ctx context.Context, serverID, eventType, actorID string, targetID *string, reason string, metadata map[string]interface{}) (*models.SystemMessage, error)
	listSystemMessagesFn        func(ctx context.Context, serverID string, before time.Time, limit int) ([]models.SystemMessage, error)
	purgeExpiredSystemMsgsFn    func(ctx context.Context, retentionDays int) (int64, error)
	getSystemMsgRetentionDaysFn func(ctx context.Context) (*int, error)

	// MLS group methods
	upsertMLSGroupInfoFn       func(ctx context.Context, channelID string, groupType string, groupInfoBytes []byte, epoch int64) error
	getMLSGroupInfoFn          func(ctx context.Context, channelID string, groupType string) ([]byte, int64, error)
	deleteMLSGroupInfoFn       func(ctx context.Context, channelID string, groupType string) error
	appendMLSCommitFn          func(ctx context.Context, channelID string, epoch int64, commitBytes []byte, senderID string) error
	getMLSCommitsSinceEpochFn  func(ctx context.Context, channelID string, sinceEpoch int64, limit int) ([]db.MLSCommitRow, error)
	purgeOldMLSCommitsFn       func(ctx context.Context, maxPerChannel int) (int64, error)
	storePendingWelcomeFn      func(ctx context.Context, channelID, recipientUserID, senderID string, welcomeBytes []byte, epoch int64) error
	getPendingWelcomesFn       func(ctx context.Context, recipientUserID string) ([]db.PendingWelcomeRow, error)
	deletePendingWelcomeFn     func(ctx context.Context, welcomeID string) error
	getVoiceKeyRotationHoursFn func(ctx context.Context) (int, error)

	// MLS guild metadata group methods
	upsertMLSGuildMetadataGroupInfoFn func(ctx context.Context, serverID string, groupInfoBytes []byte, epoch int64) error
	getMLSGuildMetadataGroupInfoFn    func(ctx context.Context, serverID string) ([]byte, int64, error)
	deleteMLSGuildMetadataGroupInfoFn func(ctx context.Context, serverID string) error

	// Guild metrics increment methods
	incrementGuildMessageCountFn func(ctx context.Context, channelID string) error
	incrementGuildMemberCountFn  func(ctx context.Context, serverID string, delta int) error
	updateGuildChannelCountsFn   func(ctx context.Context, serverID string) error

	// Transparency log methods
	insertTransparencyLogEntryFn        func(ctx context.Context, leafIndex uint64, operation string, userPubKey, subjectKey, entryCBOR, leafHash, userSig, logSig []byte) error
	getTransparencyLogEntriesByPubKeyFn func(ctx context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error)
	getLatestTransparencyTreeHeadFn     func(ctx context.Context) (*models.TransparencyTreeHead, error)
	insertTransparencyTreeHeadFn        func(ctx context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error

	// DM and discovery methods
	findDMGuildFn       func(ctx context.Context, userAID, userBID string) (*models.Server, error)
	createDMGuildFn     func(ctx context.Context, userAID, userBID string) (*models.Server, *models.Channel, error)
	discoverGuildsFn    func(ctx context.Context, category, search, sort string, page, pageSize int) ([]models.DiscoverGuild, int, error)
	searchUsersPublicFn func(ctx context.Context, query string, limit int) ([]models.UserSearchPublicResult, error)
}

func (m *mockStore) Ping(ctx context.Context) error {
	if m.pingFn != nil {
		return m.pingFn(ctx)
	}
	return nil
}

// ---------- User/session — BIP39 public-key identity ----------

func (m *mockStore) CreateUserWithPublicKey(ctx context.Context, username, displayName string, publicKey []byte) (*models.User, error) {
	if m.createUserWithPublicKeyFn != nil {
		return m.createUserWithPublicKeyFn(ctx, username, displayName, publicKey)
	}
	return nil, nil
}

func (m *mockStore) GetUserByPublicKey(ctx context.Context, publicKey []byte) (*models.User, error) {
	if m.getUserByPublicKeyFn != nil {
		return m.getUserByPublicKeyFn(ctx, publicKey)
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

// ---------- BIP39 auth nonces ----------

func (m *mockStore) InsertAuthNonce(ctx context.Context, nonce string, publicKey []byte, expiresAt time.Time) error {
	if m.insertAuthNonceFn != nil {
		return m.insertAuthNonceFn(ctx, nonce, publicKey, expiresAt)
	}
	return nil
}

func (m *mockStore) ConsumeAuthNonce(ctx context.Context, nonce string) ([]byte, error) {
	if m.consumeAuthNonceFn != nil {
		return m.consumeAuthNonceFn(ctx, nonce)
	}
	return nil, nil
}

func (m *mockStore) DeleteAuthNonce(ctx context.Context, nonce string) error {
	if m.deleteAuthNonceFn != nil {
		return m.deleteAuthNonceFn(ctx, nonce)
	}
	return nil
}

func (m *mockStore) PurgeExpiredNonces(ctx context.Context) (int64, error) {
	if m.purgeExpiredNoncesFn != nil {
		return m.purgeExpiredNoncesFn(ctx)
	}
	return 0, nil
}

// ---------- Device keys ----------

func (m *mockStore) InsertDeviceKey(ctx context.Context, userID, deviceID, label string, devicePublicKey, certificate []byte) error {
	if m.insertDeviceKeyFn != nil {
		return m.insertDeviceKeyFn(ctx, userID, deviceID, label, devicePublicKey, certificate)
	}
	return nil
}

func (m *mockStore) ListDeviceKeys(ctx context.Context, userID string) ([]models.DeviceKey, error) {
	if m.listDeviceKeysFn != nil {
		return m.listDeviceKeysFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockStore) RevokeDeviceKey(ctx context.Context, userID, deviceID string) error {
	if m.revokeDeviceKeyFn != nil {
		return m.revokeDeviceKeyFn(ctx, userID, deviceID)
	}
	return nil
}

func (m *mockStore) RevokeAllDeviceKeys(ctx context.Context, userID string) error {
	if m.revokeAllDeviceKeysFn != nil {
		return m.revokeAllDeviceKeysFn(ctx, userID)
	}
	return nil
}

func (m *mockStore) UpdateDeviceLastSeen(ctx context.Context, userID, deviceID string) error {
	if m.updateDeviceLastSeenFn != nil {
		return m.updateDeviceLastSeenFn(ctx, userID, deviceID)
	}
	return nil
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

// ---------- MLS credentials ----------

func (m *mockStore) UpsertMLSCredential(ctx context.Context, userID, deviceID string, credentialBytes, signingPublicKey []byte, identityVersion int) error {
	if m.upsertMLSCredentialFn != nil {
		return m.upsertMLSCredentialFn(ctx, userID, deviceID, credentialBytes, signingPublicKey, identityVersion)
	}
	return nil
}

func (m *mockStore) GetMLSCredential(ctx context.Context, userID, deviceID string) ([]byte, []byte, int, error) {
	if m.getMLSCredentialFn != nil {
		return m.getMLSCredentialFn(ctx, userID, deviceID)
	}
	return nil, nil, 0, nil
}

// ---------- MLS key packages ----------

func (m *mockStore) InsertMLSKeyPackages(ctx context.Context, userID, deviceID string, packages [][]byte, expiresAt time.Time) error {
	if m.insertMLSKeyPackagesFn != nil {
		return m.insertMLSKeyPackagesFn(ctx, userID, deviceID, packages, expiresAt)
	}
	return nil
}

func (m *mockStore) InsertMLSLastResortKeyPackage(ctx context.Context, userID, deviceID string, keyPackageBytes []byte) error {
	if m.insertMLSLastResortKeyPackageFn != nil {
		return m.insertMLSLastResortKeyPackageFn(ctx, userID, deviceID, keyPackageBytes)
	}
	return nil
}

func (m *mockStore) ConsumeMLSKeyPackage(ctx context.Context, userID, deviceID string) ([]byte, error) {
	if m.consumeMLSKeyPackageFn != nil {
		return m.consumeMLSKeyPackageFn(ctx, userID, deviceID)
	}
	return nil, nil
}

func (m *mockStore) CountUnusedMLSKeyPackages(ctx context.Context, userID, deviceID string) (int, error) {
	if m.countUnusedMLSKeyPackagesFn != nil {
		return m.countUnusedMLSKeyPackagesFn(ctx, userID, deviceID)
	}
	return 0, nil
}

func (m *mockStore) PurgeExpiredMLSKeyPackages(ctx context.Context) (int64, error) {
	if m.purgeExpiredMLSKeyPackagesFn != nil {
		return m.purgeExpiredMLSKeyPackagesFn(ctx)
	}
	return 0, nil
}

// ---------- Device enumeration ----------

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
		ID:                   "inst-1",
		Name:                 "Test Instance",
		RegistrationMode:     "open",
		ServerCreationPolicy: "open",
	}, nil
}

func (m *mockStore) UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string, guildDiscovery *string, serverCreationPolicy *string) error {
	if m.updateInstanceConfigFn != nil {
		return m.updateInstanceConfigFn(ctx, name, iconURL, registrationMode, guildDiscovery, serverCreationPolicy)
	}
	return nil
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

func (m *mockStore) CreateChannel(ctx context.Context, serverID string, encryptedMetadata []byte, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error) {
	if m.createChannelFn != nil {
		return m.createChannelFn(ctx, serverID, encryptedMetadata, channelType, voiceMode, parentID, position)
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

func (m *mockStore) GetChannelByTypeAndPosition(ctx context.Context, serverID, channelType string, position int) (*models.Channel, error) {
	if m.getChannelByTypeAndPositionFn != nil {
		return m.getChannelByTypeAndPositionFn(ctx, serverID, channelType, position)
	}
	return nil, nil
}

func (m *mockStore) ListServerTemplates(ctx context.Context) ([]models.ServerTemplate, error) {
	if m.listServerTemplatesFn != nil {
		return m.listServerTemplatesFn(ctx)
	}
	return []models.ServerTemplate{}, nil
}

func (m *mockStore) GetServerTemplateByID(ctx context.Context, id string) (*models.ServerTemplate, error) {
	if m.getServerTemplateByIDFn != nil {
		return m.getServerTemplateByIDFn(ctx, id)
	}
	return nil, nil
}

func (m *mockStore) GetDefaultServerTemplate(ctx context.Context) (*models.ServerTemplate, error) {
	if m.getDefaultServerTemplateFn != nil {
		return m.getDefaultServerTemplateFn(ctx)
	}
	return nil, nil
}

func (m *mockStore) CreateServerTemplate(ctx context.Context, name string, channels json.RawMessage, isDefault bool) (*models.ServerTemplate, error) {
	if m.createServerTemplateFn != nil {
		return m.createServerTemplateFn(ctx, name, channels, isDefault)
	}
	return &models.ServerTemplate{ID: uuid.New().String(), Name: name, IsDefault: isDefault}, nil
}

func (m *mockStore) UpdateServerTemplate(ctx context.Context, id string, name string, channels json.RawMessage, isDefault bool) error {
	if m.updateServerTemplateFn != nil {
		return m.updateServerTemplateFn(ctx, id, name, channels, isDefault)
	}
	return nil
}

func (m *mockStore) DeleteServerTemplate(ctx context.Context, id string) error {
	if m.deleteServerTemplateFn != nil {
		return m.deleteServerTemplateFn(ctx, id)
	}
	return nil
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

func (m *mockStore) CreateInvite(ctx context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error) {
	if m.createInviteFn != nil {
		return m.createInviteFn(ctx, serverID, code, createdBy, maxUses, expiresAt)
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

// ---------- Server / guild operations ----------

func (m *mockStore) CreateServer(ctx context.Context, encryptedMetadata []byte) (*models.Server, error) {
	if m.createServerFn != nil {
		return m.createServerFn(ctx, encryptedMetadata)
	}
	return &models.Server{
		ID:                uuid.New().String(),
		EncryptedMetadata: encryptedMetadata,
		AccessPolicy:      "open",
	}, nil
}

func (m *mockStore) UpdateServerEncryptedMetadata(ctx context.Context, serverID string, encryptedMetadata []byte) error {
	if m.updateServerEncryptedMetadataFn != nil {
		return m.updateServerEncryptedMetadataFn(ctx, serverID, encryptedMetadata)
	}
	return nil
}

func (m *mockStore) GetServerByID(ctx context.Context, serverID string) (*models.Server, error) {
	if m.getServerByIDFn != nil {
		return m.getServerByIDFn(ctx, serverID)
	}
	return nil, nil
}

func (m *mockStore) ListServersForUser(ctx context.Context, userID string) ([]models.Server, error) {
	if m.listServersForUserFn != nil {
		return m.listServersForUserFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockStore) DeleteServer(ctx context.Context, serverID string) error {
	if m.deleteServerFn != nil {
		return m.deleteServerFn(ctx, serverID)
	}
	return nil
}

func (m *mockStore) ListGuildBillingStats(ctx context.Context) ([]models.GuildBillingStats, error) {
	if m.listGuildBillingStatsFn != nil {
		return m.listGuildBillingStatsFn(ctx)
	}
	return nil, nil
}

// ---------- Server member operations ----------

func (m *mockStore) AddServerMember(ctx context.Context, serverID, userID string, permissionLevel int) error {
	if m.addServerMemberFn != nil {
		return m.addServerMemberFn(ctx, serverID, userID, permissionLevel)
	}
	return nil
}

func (m *mockStore) RemoveServerMember(ctx context.Context, serverID, userID string) error {
	if m.removeServerMemberFn != nil {
		return m.removeServerMemberFn(ctx, serverID, userID)
	}
	return nil
}

func (m *mockStore) GetServerMemberLevel(ctx context.Context, serverID, userID string) (int, error) {
	if m.getServerMemberLevelFn != nil {
		return m.getServerMemberLevelFn(ctx, serverID, userID)
	}
	return 0, nil
}

func (m *mockStore) GetServerMemberLevelByFederatedID(ctx context.Context, serverID, federatedIdentityID string) (int, error) {
	return 0, errors.New("not a guild member")
}

func (m *mockStore) AddFederatedServerMember(ctx context.Context, serverID, federatedIdentityID string, permissionLevel int) error {
	return nil
}

func (m *mockStore) GetOrCreateFederatedIdentity(ctx context.Context, publicKey []byte, homeInstance, username, displayName string) (*models.FederatedIdentity, error) {
	return nil, errors.New("not implemented")
}

func (m *mockStore) GetFederatedIdentityByPublicKey(ctx context.Context, publicKey []byte) (*models.FederatedIdentity, error) {
	return nil, nil
}

func (m *mockStore) GetFederatedIdentityByID(ctx context.Context, id string) (*models.FederatedIdentity, error) {
	return nil, nil
}

func (m *mockStore) UpdateFederatedIdentityProfile(ctx context.Context, id string, username, displayName string) error {
	return nil
}

func (m *mockStore) RemoveFederatedServerMember(ctx context.Context, serverID, federatedIdentityID string) error {
	return nil
}

func (m *mockStore) UpdateServerMemberLevel(ctx context.Context, serverID, userID string, permissionLevel int) error {
	if m.updateServerMemberLevelFn != nil {
		return m.updateServerMemberLevelFn(ctx, serverID, userID, permissionLevel)
	}
	return nil
}

func (m *mockStore) ListServerMembers(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error) {
	if m.listServerMembersFn != nil {
		return m.listServerMembersFn(ctx, serverID)
	}
	return nil, nil
}

// ---------- Moderation ----------

func (m *mockStore) InsertBan(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Ban, error) {
	if m.insertBanFn != nil {
		return m.insertBanFn(ctx, serverID, userID, actorID, reason, expiresAt)
	}
	return nil, nil
}

func (m *mockStore) GetActiveBan(ctx context.Context, serverID, userID string) (*models.Ban, error) {
	if m.getActiveBanFn != nil {
		return m.getActiveBanFn(ctx, serverID, userID)
	}
	return nil, nil
}

func (m *mockStore) LiftBan(ctx context.Context, banID, liftedByID string) error {
	if m.liftBanFn != nil {
		return m.liftBanFn(ctx, banID, liftedByID)
	}
	return nil
}

func (m *mockStore) ListActiveBans(ctx context.Context, serverID string) ([]models.Ban, error) {
	if m.listActiveBansFn != nil {
		return m.listActiveBansFn(ctx, serverID)
	}
	return nil, nil
}

func (m *mockStore) InsertMute(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Mute, error) {
	if m.insertMuteFn != nil {
		return m.insertMuteFn(ctx, serverID, userID, actorID, reason, expiresAt)
	}
	return nil, nil
}

func (m *mockStore) GetActiveMute(ctx context.Context, serverID, userID string) (*models.Mute, error) {
	if m.getActiveMuteFn != nil {
		return m.getActiveMuteFn(ctx, serverID, userID)
	}
	return nil, nil
}

func (m *mockStore) LiftMute(ctx context.Context, muteID, liftedByID string) error {
	if m.liftMuteFn != nil {
		return m.liftMuteFn(ctx, muteID, liftedByID)
	}
	return nil
}

func (m *mockStore) ListActiveMutes(ctx context.Context, serverID string) ([]models.Mute, error) {
	if m.listActiveMutesFn != nil {
		return m.listActiveMutesFn(ctx, serverID)
	}
	return nil, nil
}

func (m *mockStore) InsertAuditLog(ctx context.Context, serverID, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error {
	if m.insertAuditLogFn != nil {
		return m.insertAuditLogFn(ctx, serverID, actorID, targetID, action, reason, metadata)
	}
	return nil
}

func (m *mockStore) ListAuditLog(ctx context.Context, serverID string, limit, offset int, filter *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
	if m.listAuditLogFn != nil {
		return m.listAuditLogFn(ctx, serverID, limit, offset, filter)
	}
	return nil, nil
}

func (m *mockStore) GetMessageByID(ctx context.Context, messageID string) (*models.Message, error) {
	if m.getMessageByIDFn != nil {
		return m.getMessageByIDFn(ctx, messageID)
	}
	return nil, nil
}

func (m *mockStore) DeleteMessage(ctx context.Context, messageID string) error {
	if m.deleteMessageFn != nil {
		return m.deleteMessageFn(ctx, messageID)
	}
	return nil
}

func (m *mockStore) DeleteSessionsByUserID(ctx context.Context, userID string) error {
	if m.deleteSessionsByUserIDFn != nil {
		return m.deleteSessionsByUserIDFn(ctx, userID)
	}
	return nil
}

// ---------- Instance bans ----------

func (m *mockStore) InsertInstanceBan(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.InstanceBan, error) {
	if m.insertInstanceBanFn != nil {
		return m.insertInstanceBanFn(ctx, userID, actorID, reason, expiresAt)
	}
	return nil, nil
}

func (m *mockStore) GetActiveInstanceBan(ctx context.Context, userID string) (*models.InstanceBan, error) {
	if m.getActiveInstanceBanFn != nil {
		return m.getActiveInstanceBanFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockStore) LiftInstanceBan(ctx context.Context, banID, liftedByID string) error {
	if m.liftInstanceBanFn != nil {
		return m.liftInstanceBanFn(ctx, banID, liftedByID)
	}
	return nil
}

// ---------- Instance audit log ----------

func (m *mockStore) InsertInstanceAuditLog(ctx context.Context, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error {
	if m.insertInstanceAuditLogFn != nil {
		return m.insertInstanceAuditLogFn(ctx, actorID, targetID, action, reason, metadata)
	}
	return nil
}

func (m *mockStore) ListInstanceAuditLog(ctx context.Context, limit, offset int, filter *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
	if m.listInstanceAuditLogFn != nil {
		return m.listInstanceAuditLogFn(ctx, limit, offset, filter)
	}
	return nil, nil
}

// ---------- User search ----------

func (m *mockStore) SearchUsers(ctx context.Context, query string, limit int) ([]models.UserSearchResult, error) {
	if m.searchUsersFn != nil {
		return m.searchUsersFn(ctx, query, limit)
	}
	return nil, nil
}

// ---------- System messages ----------

func (m *mockStore) InsertSystemMessage(ctx context.Context, serverID, eventType, actorID string, targetID *string, reason string, metadata map[string]interface{}) (*models.SystemMessage, error) {
	if m.insertSystemMessageFn != nil {
		return m.insertSystemMessageFn(ctx, serverID, eventType, actorID, targetID, reason, metadata)
	}
	return &models.SystemMessage{
		ID:        uuid.New().String(),
		ServerID:  serverID,
		EventType: eventType,
		ActorID:   actorID,
		TargetID:  targetID,
		Reason:    reason,
		Metadata:  metadata,
		CreatedAt: time.Now(),
	}, nil
}

func (m *mockStore) ListSystemMessages(ctx context.Context, serverID string, before time.Time, limit int) ([]models.SystemMessage, error) {
	if m.listSystemMessagesFn != nil {
		return m.listSystemMessagesFn(ctx, serverID, before, limit)
	}
	return nil, nil
}

func (m *mockStore) PurgeExpiredSystemMessages(ctx context.Context, retentionDays int) (int64, error) {
	if m.purgeExpiredSystemMsgsFn != nil {
		return m.purgeExpiredSystemMsgsFn(ctx, retentionDays)
	}
	return 0, nil
}

func (m *mockStore) GetSystemMessageRetentionDays(ctx context.Context) (*int, error) {
	if m.getSystemMsgRetentionDaysFn != nil {
		return m.getSystemMsgRetentionDaysFn(ctx)
	}
	return nil, nil
}

// ---------- MLS group methods ----------

func (m *mockStore) UpsertMLSGroupInfo(ctx context.Context, channelID string, groupType string, groupInfoBytes []byte, epoch int64) error {
	if m.upsertMLSGroupInfoFn != nil {
		return m.upsertMLSGroupInfoFn(ctx, channelID, groupType, groupInfoBytes, epoch)
	}
	return nil
}

func (m *mockStore) GetMLSGroupInfo(ctx context.Context, channelID string, groupType string) ([]byte, int64, error) {
	if m.getMLSGroupInfoFn != nil {
		return m.getMLSGroupInfoFn(ctx, channelID, groupType)
	}
	return nil, 0, nil
}

func (m *mockStore) DeleteMLSGroupInfo(ctx context.Context, channelID string, groupType string) error {
	if m.deleteMLSGroupInfoFn != nil {
		return m.deleteMLSGroupInfoFn(ctx, channelID, groupType)
	}
	return nil
}

func (m *mockStore) AppendMLSCommit(ctx context.Context, channelID string, epoch int64, commitBytes []byte, senderID string) error {
	if m.appendMLSCommitFn != nil {
		return m.appendMLSCommitFn(ctx, channelID, epoch, commitBytes, senderID)
	}
	return nil
}

func (m *mockStore) GetMLSCommitsSinceEpoch(ctx context.Context, channelID string, sinceEpoch int64, limit int) ([]db.MLSCommitRow, error) {
	if m.getMLSCommitsSinceEpochFn != nil {
		return m.getMLSCommitsSinceEpochFn(ctx, channelID, sinceEpoch, limit)
	}
	return nil, nil
}

func (m *mockStore) PurgeOldMLSCommits(ctx context.Context, maxPerChannel int) (int64, error) {
	if m.purgeOldMLSCommitsFn != nil {
		return m.purgeOldMLSCommitsFn(ctx, maxPerChannel)
	}
	return 0, nil
}

func (m *mockStore) StorePendingWelcome(ctx context.Context, channelID, recipientUserID, senderID string, welcomeBytes []byte, epoch int64) error {
	if m.storePendingWelcomeFn != nil {
		return m.storePendingWelcomeFn(ctx, channelID, recipientUserID, senderID, welcomeBytes, epoch)
	}
	return nil
}

func (m *mockStore) GetPendingWelcomes(ctx context.Context, recipientUserID string) ([]db.PendingWelcomeRow, error) {
	if m.getPendingWelcomesFn != nil {
		return m.getPendingWelcomesFn(ctx, recipientUserID)
	}
	return nil, nil
}

func (m *mockStore) DeletePendingWelcome(ctx context.Context, welcomeID string) error {
	if m.deletePendingWelcomeFn != nil {
		return m.deletePendingWelcomeFn(ctx, welcomeID)
	}
	return nil
}

func (m *mockStore) GetVoiceKeyRotationHours(ctx context.Context) (int, error) {
	if m.getVoiceKeyRotationHoursFn != nil {
		return m.getVoiceKeyRotationHoursFn(ctx)
	}
	return 2, nil
}

// ---------- MLS guild metadata group methods ----------

func (m *mockStore) UpsertMLSGuildMetadataGroupInfo(ctx context.Context, serverID string, groupInfoBytes []byte, epoch int64) error {
	if m.upsertMLSGuildMetadataGroupInfoFn != nil {
		return m.upsertMLSGuildMetadataGroupInfoFn(ctx, serverID, groupInfoBytes, epoch)
	}
	return nil
}

func (m *mockStore) GetMLSGuildMetadataGroupInfo(ctx context.Context, serverID string) ([]byte, int64, error) {
	if m.getMLSGuildMetadataGroupInfoFn != nil {
		return m.getMLSGuildMetadataGroupInfoFn(ctx, serverID)
	}
	return nil, 0, nil
}

func (m *mockStore) DeleteMLSGuildMetadataGroupInfo(ctx context.Context, serverID string) error {
	if m.deleteMLSGuildMetadataGroupInfoFn != nil {
		return m.deleteMLSGuildMetadataGroupInfoFn(ctx, serverID)
	}
	return nil
}

// ---------- Guild metrics increment methods ----------

func (m *mockStore) IncrementGuildMessageCount(ctx context.Context, channelID string) error {
	if m.incrementGuildMessageCountFn != nil {
		return m.incrementGuildMessageCountFn(ctx, channelID)
	}
	return nil
}

func (m *mockStore) IncrementGuildMemberCount(ctx context.Context, serverID string, delta int) error {
	if m.incrementGuildMemberCountFn != nil {
		return m.incrementGuildMemberCountFn(ctx, serverID, delta)
	}
	return nil
}

func (m *mockStore) UpdateGuildChannelCounts(ctx context.Context, serverID string) error {
	if m.updateGuildChannelCountsFn != nil {
		return m.updateGuildChannelCountsFn(ctx, serverID)
	}
	return nil
}

// ---------- Transparency log ----------

func (m *mockStore) InsertTransparencyLogEntry(ctx context.Context, leafIndex uint64, operation string, userPubKey, subjectKey, entryCBOR, leafHash, userSig, logSig []byte) error {
	if m.insertTransparencyLogEntryFn != nil {
		return m.insertTransparencyLogEntryFn(ctx, leafIndex, operation, userPubKey, subjectKey, entryCBOR, leafHash, userSig, logSig)
	}
	return nil
}

func (m *mockStore) GetTransparencyLogEntriesByPubKey(ctx context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error) {
	if m.getTransparencyLogEntriesByPubKeyFn != nil {
		return m.getTransparencyLogEntriesByPubKeyFn(ctx, pubKey)
	}
	return nil, nil
}

func (m *mockStore) GetLatestTransparencyTreeHead(ctx context.Context) (*models.TransparencyTreeHead, error) {
	if m.getLatestTransparencyTreeHeadFn != nil {
		return m.getLatestTransparencyTreeHeadFn(ctx)
	}
	return nil, nil
}

func (m *mockStore) InsertTransparencyTreeHead(ctx context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error {
	if m.insertTransparencyTreeHeadFn != nil {
		return m.insertTransparencyTreeHeadFn(ctx, treeSize, rootHash, fringe, headSig)
	}
	return nil
}

// ---------- DM and discovery methods ----------

func (m *mockStore) FindDMGuild(ctx context.Context, userAID, userBID string) (*models.Server, error) {
	if m.findDMGuildFn != nil {
		return m.findDMGuildFn(ctx, userAID, userBID)
	}
	return nil, nil
}

func (m *mockStore) CreateDMGuild(ctx context.Context, userAID, userBID string) (*models.Server, *models.Channel, error) {
	if m.createDMGuildFn != nil {
		return m.createDMGuildFn(ctx, userAID, userBID)
	}
	return nil, nil, nil
}

func (m *mockStore) DiscoverGuilds(ctx context.Context, category, search, sort string, page, pageSize int) ([]models.DiscoverGuild, int, error) {
	if m.discoverGuildsFn != nil {
		return m.discoverGuildsFn(ctx, category, search, sort, page, pageSize)
	}
	return []models.DiscoverGuild{}, 0, nil
}

func (m *mockStore) SearchUsersPublic(ctx context.Context, query string, limit int) ([]models.UserSearchPublicResult, error) {
	if m.searchUsersPublicFn != nil {
		return m.searchUsersPublicFn(ctx, query, limit)
	}
	return []models.UserSearchPublicResult{}, nil
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

// makeServerAuth is an alias for makeAuth, kept for backward compatibility.
func makeServerAuth(store *mockStore, userID string) string {
	return makeAuth(store, userID)
}

// makeGuildAuth sets up auth AND guild membership mock for the given permission level.
// guildRole is a legacy string ("owner", "admin", "mod", "member") mapped to an int level.
// Returns the bearer token. Useful for tests that need to pass RequireGuildMember.
func makeGuildAuth(store *mockStore, userID, guildRole string) string {
	token := makeAuth(store, userID)
	levelMap := map[string]int{
		"owner":  3,
		"admin":  2,
		"mod":    1,
		"member": 0,
	}
	level := levelMap[guildRole]
	store.getServerMemberLevelFn = func(_ context.Context, _, uid string) (int, error) {
		if uid == userID {
			return level, nil
		}
		return 0, nil
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

// guildLevelFromRoleName maps legacy role name strings to permission level integers.
// Used in tests that were written before the integer permission level model.
func guildLevelFromRoleName(role string) int {
	switch role {
	case "owner":
		return models.PermissionLevelOwner
	case "admin":
		return models.PermissionLevelAdmin
	case "mod":
		return models.PermissionLevelMod
	default:
		return models.PermissionLevelMember
	}
}
