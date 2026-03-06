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
	upsertIdentityKeysFn        func(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error
	insertOneTimePreKeysFn       func(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error
	getIdentityAndSignedPreKeyFn func(ctx context.Context, userID, deviceID string) (identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int, err error)
	consumeOneTimePreKeyFn       func(ctx context.Context, userID, deviceID string) (keyID int, publicKey []byte, err error)
	countUnusedOneTimePreKeysFn  func(ctx context.Context, userID, deviceID string) (int, error)
	listDeviceIDsForUserFn       func(ctx context.Context, userID string) ([]string, error)
	upsertDeviceFn               func(ctx context.Context, userID, deviceID, label string) error

	// Messages
	insertMessageFn   func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	getMessagesFn     func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn func(ctx context.Context, channelID, userID string) (bool, error)

	// Instance
	getInstanceConfigFn    func(ctx context.Context) (*models.InstanceConfig, error)
	updateInstanceConfigFn func(ctx context.Context, name *string, iconURL *string, registrationMode *string, serverCreationPolicy *string) error
	setInstanceOwnerFn     func(ctx context.Context, userID string) (bool, error)
	getUserRoleFn          func(ctx context.Context, userID string) (string, error)
	updateUserRoleFn       func(ctx context.Context, userID, role string) error
	listMembersFn          func(ctx context.Context) ([]models.Member, error)

	// Channels (guild-scoped — serverID param)
	createChannelFn  func(ctx context.Context, serverID, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	listChannelsFn   func(ctx context.Context, serverID string) ([]models.Channel, error)
	getChannelByIDFn func(ctx context.Context, channelID string) (*models.Channel, error)
	deleteChannelFn  func(ctx context.Context, channelID string) error
	moveChannelFn    func(ctx context.Context, channelID string, parentID *string, position int) error

	// Invites (guild-scoped — serverID param)
	createInviteFn    func(ctx context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	getInviteByCodeFn func(ctx context.Context, code string) (*models.InviteCode, error)
	claimInviteUseFn  func(ctx context.Context, code string) (bool, error)

	// Server / guild operations
	createServerFn          func(ctx context.Context, name, ownerID string) (*models.Server, error)
	getServerByIDFn         func(ctx context.Context, serverID string) (*models.Server, error)
	listServersForUserFn    func(ctx context.Context, userID string) ([]models.Server, error)
	deleteServerFn          func(ctx context.Context, serverID string) error
	listGuildBillingStatsFn func(ctx context.Context) ([]models.GuildBillingStats, error)

	// Server member operations
	addServerMemberFn        func(ctx context.Context, serverID, userID, role string) error
	removeServerMemberFn     func(ctx context.Context, serverID, userID string) error
	getServerMemberRoleFn    func(ctx context.Context, serverID, userID string) (string, error)
	updateServerMemberRoleFn func(ctx context.Context, serverID, userID, role string) error
	listServerMembersFn      func(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error)

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

func (m *mockStore) UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string, serverCreationPolicy *string) error {
	if m.updateInstanceConfigFn != nil {
		return m.updateInstanceConfigFn(ctx, name, iconURL, registrationMode, serverCreationPolicy)
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

func (m *mockStore) CreateServer(ctx context.Context, name, ownerID string) (*models.Server, error) {
	if m.createServerFn != nil {
		return m.createServerFn(ctx, name, ownerID)
	}
	return &models.Server{ID: uuid.New().String(), Name: name, OwnerID: ownerID}, nil
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

func (m *mockStore) GetServerMemberRole(ctx context.Context, serverID, userID string) (string, error) {
	if m.getServerMemberRoleFn != nil {
		return m.getServerMemberRoleFn(ctx, serverID, userID)
	}
	return "", nil
}

func (m *mockStore) UpdateServerMemberRole(ctx context.Context, serverID, userID, role string) error {
	if m.updateServerMemberRoleFn != nil {
		return m.updateServerMemberRoleFn(ctx, serverID, userID, role)
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

// makeGuildAuth sets up auth AND guild membership mock for the given role.
// Returns the bearer token. Useful for tests that need to pass RequireGuildMember.
func makeGuildAuth(store *mockStore, userID, guildRole string) string {
	token := makeAuth(store, userID)
	store.getServerMemberRoleFn = func(_ context.Context, _, uid string) (string, error) {
		if uid == userID {
			return guildRole, nil
		}
		return "", nil
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
