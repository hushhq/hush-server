package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compile-time interface check: messageStoreMock must satisfy db.Store.
var _ db.Store = (*messageStoreMock)(nil)

// messageStoreMock implements db.Store for message handler tests. Only message methods are used.
type messageStoreMock struct {
	insertMessageFn                     func(ctx context.Context, channelID string, senderID *string, federatedSenderID *string, recipientID *string, ciphertext []byte) (*models.Message, error)
	getMessagesFn                       func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	getMessagesAfterFn                  func(ctx context.Context, channelID, recipientID string, after time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn                   func(ctx context.Context, channelID, userID string) (bool, error)
	markChannelReadFn                   func(ctx context.Context, channelID, userID, messageID string) error
	getServerMemberLevelFn              func(ctx context.Context, serverID, userID string) (int, error)
	getServerMemberLevelByFederatedIDFn func(ctx context.Context, serverID, federatedIdentityID string) (int, error)
}

// Ping stub.
func (m *messageStoreMock) Ping(context.Context) error { return nil }

// User/session stubs (unused in ws handler tests).
func (m *messageStoreMock) CreateUserWithPublicKey(context.Context, string, string, []byte) (*models.User, error) {
	return nil, nil
}
func (m *messageStoreMock) GetUserByPublicKey(context.Context, []byte) (*models.User, error) {
	return nil, nil
}
func (m *messageStoreMock) GetUserByUsername(context.Context, string) (*models.User, error) {
	return nil, nil
}
func (m *messageStoreMock) GetUserByID(context.Context, string) (*models.User, error) {
	return nil, nil
}

// BIP39 auth nonce stubs.
func (m *messageStoreMock) InsertAuthNonce(context.Context, string, []byte, time.Time) error {
	return nil
}
func (m *messageStoreMock) ConsumeAuthNonce(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteAuthNonce(context.Context, string) error     { return nil }
func (m *messageStoreMock) PurgeExpiredNonces(context.Context) (int64, error) { return 0, nil }

// Device key stubs.
func (m *messageStoreMock) InsertDeviceKey(context.Context, string, string, string, []byte, []byte) error {
	return nil
}
func (m *messageStoreMock) BackfillRootDeviceKey(context.Context, string, string, []byte) (bool, error) {
	return true, nil
}
func (m *messageStoreMock) ListDeviceKeys(context.Context, string) ([]models.DeviceKey, error) {
	return nil, nil
}
func (m *messageStoreMock) RevokeDeviceKey(context.Context, string, string) error { return nil }
func (m *messageStoreMock) IsDeviceActive(context.Context, string, string) (bool, error) {
	return true, nil
}
func (m *messageStoreMock) RevokeAllDeviceKeys(context.Context, string) error          { return nil }
func (m *messageStoreMock) UpdateDeviceLastSeen(context.Context, string, string) error { return nil }
func (m *messageStoreMock) CreateSession(context.Context, string, string, string, time.Time) (*models.Session, error) {
	return nil, nil
}
func (m *messageStoreMock) GetSessionByTokenHash(context.Context, string) (*models.Session, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteSessionByID(context.Context, string) error     { return nil }
func (m *messageStoreMock) PurgeExpiredSessions(context.Context) (int64, error) { return 0, nil }
func (m *messageStoreMock) PurgeStaleAdminSessions(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) CountInstanceAdmins(context.Context) (int, error) { return 0, nil }
func (m *messageStoreMock) CreateInstanceAdmin(context.Context, string, *string, string, string) (*models.InstanceAdmin, error) {
	return nil, nil
}
func (m *messageStoreMock) GetInstanceAdminByUsername(context.Context, string) (*models.InstanceAdmin, error) {
	return nil, nil
}
func (m *messageStoreMock) GetInstanceAdminByID(context.Context, string) (*models.InstanceAdmin, error) {
	return nil, nil
}
func (m *messageStoreMock) ListInstanceAdmins(context.Context) ([]models.InstanceAdmin, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateInstanceAdmin(context.Context, string, *string, string, bool) (*models.InstanceAdmin, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateInstanceAdminPassword(context.Context, string, string) error {
	return nil
}
func (m *messageStoreMock) TouchInstanceAdminLastLogin(context.Context, string, time.Time) error {
	return nil
}
func (m *messageStoreMock) CreateInstanceAdminSession(context.Context, string, string, string, time.Time, *string, *string) (*models.InstanceAdminSession, error) {
	return nil, nil
}
func (m *messageStoreMock) GetInstanceAdminSessionByTokenHash(context.Context, string) (*models.InstanceAdminSession, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteInstanceAdminSessionByID(context.Context, string) error { return nil }
func (m *messageStoreMock) UpdateInstanceAdminSessionLastSeen(context.Context, string, time.Time) error {
	return nil
}
func (m *messageStoreMock) GetInstanceServiceIdentity(context.Context) (*models.InstanceServiceIdentity, error) {
	return nil, nil
}
func (m *messageStoreMock) UpsertInstanceServiceIdentity(context.Context, string, []byte, []byte, string) (*models.InstanceServiceIdentity, error) {
	return nil, nil
}

// MLS credential stubs.
func (m *messageStoreMock) UpsertMLSCredential(context.Context, string, string, []byte, []byte, int) error {
	return nil
}
func (m *messageStoreMock) GetMLSCredential(context.Context, string, string) ([]byte, []byte, int, error) {
	return nil, nil, 0, nil
}

// MLS key package stubs.
func (m *messageStoreMock) InsertMLSKeyPackages(context.Context, string, string, [][]byte, time.Time) error {
	return nil
}
func (m *messageStoreMock) InsertMLSLastResortKeyPackage(context.Context, string, string, []byte) error {
	return nil
}
func (m *messageStoreMock) ConsumeMLSKeyPackage(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (m *messageStoreMock) CountUnusedMLSKeyPackages(context.Context, string, string) (int, error) {
	return 0, nil
}
func (m *messageStoreMock) PurgeExpiredMLSKeyPackages(context.Context) (int64, error) { return 0, nil }

// Device enumeration stubs.
func (m *messageStoreMock) ListDeviceIDsForUser(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *messageStoreMock) UpsertDevice(context.Context, string, string, string) error { return nil }

// Message methods (actually used).
func (m *messageStoreMock) InsertMessage(ctx context.Context, channelID string, senderID *string, federatedSenderID *string, recipientID *string, ciphertext []byte) (*models.Message, error) {
	if m.insertMessageFn != nil {
		return m.insertMessageFn(ctx, channelID, senderID, federatedSenderID, recipientID, ciphertext)
	}
	return nil, nil
}
func (m *messageStoreMock) GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error) {
	if m.getMessagesFn != nil {
		return m.getMessagesFn(ctx, channelID, recipientID, before, limit)
	}
	return nil, nil
}
func (m *messageStoreMock) GetMessagesAfter(ctx context.Context, channelID, recipientID string, after time.Time, limit int) ([]models.Message, error) {
	if m.getMessagesAfterFn != nil {
		return m.getMessagesAfterFn(ctx, channelID, recipientID, after, limit)
	}
	return nil, nil
}
func (m *messageStoreMock) IsChannelMember(ctx context.Context, channelID, userID string) (bool, error) {
	if m.isChannelMemberFn != nil {
		return m.isChannelMemberFn(ctx, channelID, userID)
	}
	return false, nil
}

// Instance stubs.
func (m *messageStoreMock) GetInstanceConfig(context.Context) (*models.InstanceConfig, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateInstanceConfig(context.Context, *string, *string, *string, *string, *string, *int, *int, *int, *string, *int64, *int64, *int) error {
	return nil
}
func (m *messageStoreMock) GetUserRole(context.Context, string) (string, error)  { return "member", nil }
func (m *messageStoreMock) UpdateUserRole(context.Context, string, string) error { return nil }
func (m *messageStoreMock) ListMembers(context.Context) ([]models.Member, error) { return nil, nil }

// Channel stubs (guild-scoped - serverID param).
// CreateChannel now takes encryptedMetadata []byte instead of a plaintext name.
func (m *messageStoreMock) CreateChannel(context.Context, string, []byte, string, *string, int) (*models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) ListChannels(context.Context, string) ([]models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) GetChannelByID(context.Context, string) (*models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteChannelTree(context.Context, string, string) ([]string, []string, error) {
	return nil, nil, nil
}
func (m *messageStoreMock) GetChannelByTypeAndPosition(context.Context, string, string, int) (*models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteChannel(context.Context, string, string) error { return nil }
func (m *messageStoreMock) MoveChannel(context.Context, string, string, *string, int) error {
	return nil
}
func (m *messageStoreMock) InsertAttachment(context.Context, string, string, string, string, int64) (*models.Attachment, error) {
	return nil, nil
}
func (m *messageStoreMock) GetAttachmentByID(context.Context, string) (*models.Attachment, error) {
	return nil, nil
}
func (m *messageStoreMock) SoftDeleteAttachment(context.Context, string, string) (*models.Attachment, error) {
	return nil, nil
}
func (m *messageStoreMock) SoftDeleteAttachmentsByID(context.Context, []string) ([]models.Attachment, error) {
	return nil, nil
}
func (m *messageStoreMock) ListAttachmentsForGuildQuota(context.Context, string) (string, []models.Attachment, error) {
	return "", nil, nil
}
func (m *messageStoreMock) ListExpiredAttachments(context.Context, int, int) ([]models.Attachment, error) {
	return nil, nil
}
func (m *messageStoreMock) PurgeExpiredMessages(context.Context, int) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) ListServerTemplates(context.Context) ([]models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) GetServerTemplateByID(context.Context, string) (*models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) GetDefaultServerTemplate(context.Context) (*models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) CreateServerTemplate(context.Context, string, json.RawMessage, bool) (*models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateServerTemplate(context.Context, string, string, json.RawMessage, bool) error {
	return nil
}
func (m *messageStoreMock) DeleteServerTemplate(context.Context, string) error { return nil }

// Invite stubs (guild-scoped - serverID param).
func (m *messageStoreMock) CreateInvite(context.Context, string, string, string, int, time.Time) (*models.InviteCode, error) {
	return nil, nil
}
func (m *messageStoreMock) GetInviteByCode(context.Context, string) (*models.InviteCode, error) {
	return nil, nil
}
func (m *messageStoreMock) ClaimInviteUse(context.Context, string) (bool, error) { return true, nil }

// Server / guild operation stubs.
func (m *messageStoreMock) CountOwnedServers(context.Context, string) (int, error) { return 0, nil }
func (m *messageStoreMock) UpdateServerMemberCapOverride(context.Context, string, *int) error {
	return nil
}

// CreateServer now takes encryptedMetadata []byte only (no plaintext name).
func (m *messageStoreMock) CreateServer(context.Context, []byte) (*models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateServerEncryptedMetadata(context.Context, string, []byte) error {
	return nil
}
func (m *messageStoreMock) GetServerByID(context.Context, string) (*models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) ListServersForUser(context.Context, string) ([]models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteServer(context.Context, string) error { return nil }
func (m *messageStoreMock) ListGuildBillingStats(context.Context) ([]models.GuildBillingStats, error) {
	return nil, nil
}

// Server member operation stubs.
// AddServerMember uses permissionLevel int instead of role string.
func (m *messageStoreMock) AddServerMember(context.Context, string, string, int) error { return nil }
func (m *messageStoreMock) RemoveServerMember(context.Context, string, string) error   { return nil }
func (m *messageStoreMock) GetServerMemberLevel(ctx context.Context, serverID, userID string) (int, error) {
	if m.getServerMemberLevelFn != nil {
		return m.getServerMemberLevelFn(ctx, serverID, userID)
	}
	return 0, nil
}
func (m *messageStoreMock) UpdateServerMemberLevel(context.Context, string, string, int) error {
	return nil
}
func (m *messageStoreMock) ListServerMembers(context.Context, string) ([]models.ServerMemberWithUser, error) {
	return nil, nil
}

// Moderation stubs (guild-scoped - serverID param).
func (m *messageStoreMock) InsertBan(context.Context, string, string, string, string, *time.Time) (*models.Ban, error) {
	return nil, nil
}
func (m *messageStoreMock) GetActiveBan(context.Context, string, string) (*models.Ban, error) {
	return nil, nil
}
func (m *messageStoreMock) LiftBan(context.Context, string, string) error { return nil }
func (m *messageStoreMock) ListActiveBans(context.Context, string) ([]models.Ban, error) {
	return nil, nil
}
func (m *messageStoreMock) InsertMute(context.Context, string, string, string, string, *time.Time) (*models.Mute, error) {
	return nil, nil
}
func (m *messageStoreMock) GetActiveMute(context.Context, string, string) (*models.Mute, error) {
	return nil, nil
}
func (m *messageStoreMock) LiftMute(context.Context, string, string) error { return nil }
func (m *messageStoreMock) ListActiveMutes(context.Context, string) ([]models.Mute, error) {
	return nil, nil
}
func (m *messageStoreMock) InsertAuditLog(context.Context, string, string, *string, string, string, map[string]interface{}) error {
	return nil
}
func (m *messageStoreMock) ListAuditLog(_ context.Context, _ string, _, _ int, _ *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
	return nil, nil
}
func (m *messageStoreMock) GetMessageByID(context.Context, string) (*models.Message, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteMessage(context.Context, string, string) error  { return nil }
func (m *messageStoreMock) DeleteSessionsByUserID(context.Context, string) error { return nil }

// Instance ban stubs.
func (m *messageStoreMock) InsertInstanceBan(context.Context, string, string, string, *time.Time) (*models.InstanceBan, error) {
	return nil, nil
}
func (m *messageStoreMock) InsertInstanceBanByAdmin(context.Context, string, string, string, *time.Time) (*models.InstanceBan, error) {
	return nil, nil
}
func (m *messageStoreMock) GetActiveInstanceBan(context.Context, string) (*models.InstanceBan, error) {
	return nil, nil
}
func (m *messageStoreMock) LiftInstanceBan(context.Context, string, string) error { return nil }
func (m *messageStoreMock) LiftInstanceBanByAdmin(context.Context, string, string) error {
	return nil
}

// Instance audit log stubs.
func (m *messageStoreMock) InsertInstanceAuditLog(context.Context, string, *string, string, string, map[string]interface{}) error {
	return nil
}
func (m *messageStoreMock) ListInstanceAuditLog(_ context.Context, _, _ int, _ *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
	return nil, nil
}

// User search stub.
func (m *messageStoreMock) SearchUsers(context.Context, string, int) ([]models.UserSearchResult, error) {
	return nil, nil
}

// System messages stubs.
func (m *messageStoreMock) InsertSystemMessage(context.Context, string, string, string, *string, string, map[string]interface{}) (*models.SystemMessage, error) {
	return nil, nil
}
func (m *messageStoreMock) ListSystemMessages(context.Context, string, time.Time, int) ([]models.SystemMessage, error) {
	return nil, nil
}
func (m *messageStoreMock) PurgeExpiredSystemMessages(context.Context, int) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) GetSystemMessageRetentionDays(context.Context) (*int, error) {
	return nil, nil
}

// MLS group stubs (groupType added in M.3-01).
func (m *messageStoreMock) UpsertMLSGroupInfo(_ context.Context, _ string, _ string, _ []byte, _ int64) error {
	return nil
}
func (m *messageStoreMock) GetMLSGroupInfo(_ context.Context, _ string, _ string) ([]byte, int64, error) {
	return nil, 0, nil
}
func (m *messageStoreMock) DeleteMLSGroupInfo(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *messageStoreMock) AppendMLSCommit(context.Context, string, int64, []byte, string) error {
	return nil
}
func (m *messageStoreMock) GetMLSCommitsSinceEpoch(context.Context, string, int64, int) ([]db.MLSCommitRow, error) {
	return nil, nil
}
func (m *messageStoreMock) PurgeOldMLSCommits(context.Context, int) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) StorePendingWelcome(context.Context, string, string, string, []byte, int64) error {
	return nil
}
func (m *messageStoreMock) GetPendingWelcomes(context.Context, string) ([]db.PendingWelcomeRow, error) {
	return nil, nil
}
func (m *messageStoreMock) DeletePendingWelcome(context.Context, string) error    { return nil }
func (m *messageStoreMock) GetVoiceKeyRotationHours(context.Context) (int, error) { return 2, nil }

// Guild metadata GroupInfo stubs (0O-03: encrypted guild name/icon blob).
func (m *messageStoreMock) UpsertMLSGuildMetadataGroupInfo(context.Context, string, []byte, int64) error {
	return nil
}
func (m *messageStoreMock) GetMLSGuildMetadataGroupInfo(context.Context, string) ([]byte, int64, error) {
	return nil, 0, nil
}
func (m *messageStoreMock) DeleteMLSGuildMetadataGroupInfo(context.Context, string) error { return nil }

// Guild counter stubs (0O-03: activity tracking).
func (m *messageStoreMock) IncrementGuildMessageCount(context.Context, string) error     { return nil }
func (m *messageStoreMock) IncrementGuildMemberCount(context.Context, string, int) error { return nil }
func (m *messageStoreMock) UpdateGuildChannelCounts(context.Context, string) error       { return nil }

// Transparency log stubs (T.1).
func (m *messageStoreMock) InsertTransparencyLogEntry(context.Context, uint64, string, []byte, []byte, []byte, []byte, []byte, []byte) error {
	return nil
}
func (m *messageStoreMock) GetTransparencyLogEntriesByPubKey(context.Context, []byte) ([]models.TransparencyLogEntry, error) {
	return nil, nil
}
func (m *messageStoreMock) GetLatestTransparencyTreeHead(context.Context) (*models.TransparencyTreeHead, error) {
	return nil, nil
}
func (m *messageStoreMock) InsertTransparencyTreeHead(context.Context, uint64, []byte, []byte, []byte) error {
	return nil
}

// Read marker stubs (GC.3).
func (m *messageStoreMock) GetUnreadCount(context.Context, string, string) (int, error) {
	return 0, nil
}
func (m *messageStoreMock) MarkChannelRead(ctx context.Context, channelID, userID, messageID string) error {
	if m.markChannelReadFn != nil {
		return m.markChannelReadFn(ctx, channelID, userID, messageID)
	}
	return nil
}

// DM and discovery stubs (GC.2-01).
func (m *messageStoreMock) FindDMGuild(context.Context, string, string) (*models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) CreateDMGuild(context.Context, string, string) (*models.Server, *models.Channel, error) {
	return nil, nil, nil
}
func (m *messageStoreMock) DiscoverGuilds(context.Context, string, string, string, int, int) ([]models.DiscoverGuild, int, error) {
	return nil, 0, nil
}
func (m *messageStoreMock) SearchUsersPublic(context.Context, string, int) ([]models.UserSearchPublicResult, error) {
	return nil, nil
}

// Federated identity stubs.
func (m *messageStoreMock) GetOrCreateFederatedIdentity(context.Context, []byte, string, string, string) (*models.FederatedIdentity, error) {
	return nil, nil
}
func (m *messageStoreMock) GetFederatedIdentityByPublicKey(context.Context, []byte) (*models.FederatedIdentity, error) {
	return nil, nil
}
func (m *messageStoreMock) GetFederatedIdentityByID(context.Context, string) (*models.FederatedIdentity, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateFederatedIdentityProfile(context.Context, string, string, string) error {
	return nil
}
func (m *messageStoreMock) AddFederatedServerMember(context.Context, string, string, int) error {
	return nil
}
func (m *messageStoreMock) RemoveFederatedServerMember(context.Context, string, string) error {
	return nil
}
func (m *messageStoreMock) GetServerMemberLevelByFederatedID(ctx context.Context, serverID, federatedIdentityID string) (int, error) {
	if m.getServerMemberLevelByFederatedIDFn != nil {
		return m.getServerMemberLevelByFederatedIDFn(ctx, serverID, federatedIdentityID)
	}
	return 0, nil
}

// Link archive methods - not used by message handlers, stubs only.
func (m *messageStoreMock) InsertLinkArchive(context.Context, db.LinkArchiveInsert) (*db.LinkArchive, error) {
	return nil, nil
}
func (m *messageStoreMock) CountActiveLinkArchivesForUser(context.Context, string) (int, error) {
	return 0, nil
}
func (m *messageStoreMock) ListSupersedableLinkArchivesForUser(context.Context, string, time.Time) ([]string, error) {
	return nil, nil
}
func (m *messageStoreMock) SumActiveLinkArchiveBytes(context.Context) (int64, error) { return 0, nil }
func (m *messageStoreMock) TransitionLinkArchiveState(context.Context, string, string, []string) error {
	return nil
}
func (m *messageStoreMock) GetLinkArchiveByID(context.Context, string) (*db.LinkArchive, error) {
	return nil, nil
}
func (m *messageStoreMock) GetLinkArchiveByUploadTokenHash(context.Context, string, []byte) (*db.LinkArchive, error) {
	return nil, nil
}
func (m *messageStoreMock) GetLinkArchiveByDownloadTokenHash(context.Context, string, []byte) (*db.LinkArchive, error) {
	return nil, nil
}
func (m *messageStoreMock) RefreshLinkArchiveExpiry(context.Context, string, time.Duration) (time.Time, error) {
	return time.Time{}, nil
}
func (m *messageStoreMock) InsertLinkArchiveChunk(context.Context, db.LinkArchiveChunkInsert) error {
	return nil
}
func (m *messageStoreMock) GetLinkArchiveChunkPointer(context.Context, string, int) (string, string, error) {
	return "", "", nil
}
func (m *messageStoreMock) ListLinkArchiveChunkRows(context.Context, string) ([]db.LinkArchiveChunkRow, error) {
	return nil, nil
}
func (m *messageStoreMock) MarkLinkArchiveFinalized(context.Context, string) error { return nil }
func (m *messageStoreMock) DeleteLinkArchive(context.Context, string) error        { return nil }
func (m *messageStoreMock) ListGcEligibleLinkArchives(context.Context, int) ([]string, error) {
	return nil, nil
}
func (m *messageStoreMock) PurgeExpiredLinkArchives(context.Context) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) UpsertChunkBlob(context.Context, string, []byte) error { return nil }
func (m *messageStoreMock) GetChunkBlob(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteChunkBlob(context.Context, string) error         { return nil }
func (m *messageStoreMock) ChunkBlobExists(context.Context, string) (bool, error) { return false, nil }

// drainUntilType reads from c.send until a message with the given type is received or timeout.
func drainUntilType(t *testing.T, c *Client, wantType string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-c.send:
			var out struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(msg, &out); err != nil {
				continue
			}
			if out.Type == wantType {
				return msg
			}
		case <-deadline:
			t.Fatalf("timed out waiting for type %q", wantType)
			return nil
		}
	}
}

func TestMessageHandler_HandleMessageSend_ForbiddenWhenNotMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) {
			return false, nil
		},
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "ciphertext": "YWVz"})
	h.Handle(c, "message.send", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "error", out.Type)
	assert.Equal(t, "forbidden", out.Code)
}

func TestMessageHandler_HandleMessageSend_StoresAndBroadcasts(t *testing.T) {
	hub := NewHub()
	var inserted *models.Message
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return true, nil },
		insertMessageFn: func(ctx context.Context, channelID string, senderID *string, federatedSenderID *string, recipientID *string, ciphertext []byte) (*models.Message, error) {
			inserted = &models.Message{
				ID:         "msg-1",
				ChannelID:  channelID,
				SenderID:   senderID,
				Ciphertext: ciphertext,
				Timestamp:  time.Now(),
			}
			return inserted, nil
		},
	}
	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "ciphertext": "YWVz"})
	h.Handle(sender, "message.send", raw)

	require.NotNil(t, inserted)
	assert.Equal(t, "ch1", inserted.ChannelID)
	require.NotNil(t, inserted.SenderID)
	assert.Equal(t, "user1", *inserted.SenderID)

	msg := drainUntilType(t, recv, "message.new", time.Second)
	{
		var out struct {
			Type           string `json:"type"`
			ID             string `json:"id"`
			ChannelID      string `json:"channel_id"`
			SenderID       string `json:"sender_id"`
			SenderDeviceID string `json:"sender_device_id"`
		}
		require.NoError(t, json.Unmarshal(msg, &out))
		assert.Equal(t, "message.new", out.Type)
		assert.Equal(t, "msg-1", out.ID)
		assert.Equal(t, "ch1", out.ChannelID)
		assert.Equal(t, "user1", out.SenderID)
		assert.Equal(t, "device-1", out.SenderDeviceID)
	}
}

func TestMessageHandler_HandleMessageHistory_ForbiddenWhenNotMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return false, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "message.history", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct{ Type, Code string }
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "error", out.Type)
	assert.Equal(t, "forbidden", out.Code)
}

func TestMessageHandler_HandleMessageHistory_ReturnsMessages(t *testing.T) {
	hub := NewHub()
	u1 := "u1"
	msgs := []models.Message{
		{ID: "m1", ChannelID: "ch1", SenderID: &u1, Ciphertext: []byte("a"), Timestamp: time.Now()},
	}
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return true, nil },
		getMessagesFn: func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error) {
			return msgs, nil
		},
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "message.history", raw)

	msg := drainUntilType(t, c, "message.history.response", time.Second)
	var resp struct {
		Type     string `json:"type"`
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(msg, &resp))
	assert.Equal(t, "message.history.response", resp.Type)
	require.Len(t, resp.Messages, 1)
	assert.Equal(t, "m1", resp.Messages[0].ID)
}

func TestMessageHandler_HandleMessageSend_FanoutStoresAndBroadcastsPerRecipient(t *testing.T) {
	hub := NewHub()
	var insertCount int
	var lastRecipientID *string
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
		insertMessageFn: func(ctx context.Context, channelID string, senderID *string, federatedSenderID *string, recipientID *string, ciphertext []byte) (*models.Message, error) {
			insertCount++
			lastRecipientID = recipientID
			return &models.Message{ID: "msg-" + *recipientID, ChannelID: channelID, SenderID: senderID, Ciphertext: ciphertext, Timestamp: time.Now()}, nil
		},
	}
	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	raw, _ := json.Marshal(map[string]interface{}{
		"channel_id":              "ch1",
		"ciphertext_by_recipient": map[string]string{"user2": "YWVz", "user3": "eHl6"},
	})
	h.Handle(sender, "message.send", raw)

	// 2 recipient inserts + 1 sender copy = 3
	assert.Equal(t, 3, insertCount)
	// Last insert is the sender copy (recipient_id = sender)
	assert.NotNil(t, lastRecipientID)
	assert.Equal(t, "user1", *lastRecipientID)

	// user2 should receive only their own ciphertext, not user3's
	msg := drainUntilType(t, recv, "message.new", time.Second)
	var out struct {
		Type           string `json:"type"`
		ID             string `json:"id"`
		ChannelID      string `json:"channel_id"`
		SenderID       string `json:"sender_id"`
		SenderDeviceID string `json:"sender_device_id"`
		Ciphertext     string `json:"ciphertext"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "message.new", out.Type)
	assert.Equal(t, "ch1", out.ChannelID)
	assert.Equal(t, "user1", out.SenderID)
	assert.Equal(t, "device-1", out.SenderDeviceID)
	assert.Equal(t, "YWVz", out.Ciphertext)
	assert.NotEmpty(t, out.ID)

	// sender should receive a self-echo for the sender copy (no ciphertext)
	selfEcho := drainUntilType(t, sender, "message.new", time.Second)
	var echoOut struct {
		Type           string `json:"type"`
		ID             string `json:"id"`
		ChannelID      string `json:"channel_id"`
		SenderID       string `json:"sender_id"`
		SenderDeviceID string `json:"sender_device_id"`
	}
	require.NoError(t, json.Unmarshal(selfEcho, &echoOut))
	assert.Equal(t, "message.new", echoOut.Type)
	assert.Equal(t, "ch1", echoOut.ChannelID)
	assert.Equal(t, "user1", echoOut.SenderID)
	assert.Equal(t, "device-1", echoOut.SenderDeviceID)
	assert.Equal(t, "msg-user1", echoOut.ID)
}

func TestMessageHandler_HandleMessageSend_FanoutDoesNotMarkRead(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
		insertMessageFn: func(_ context.Context, channelID string, senderID *string, _ *string, recipientID *string, ciphertext []byte) (*models.Message, error) {
			id := "msg-send"
			if recipientID != nil {
				id = "msg-" + *recipientID
			}
			return &models.Message{ID: id, ChannelID: channelID, SenderID: senderID, Ciphertext: ciphertext, Timestamp: time.Now()}, nil
		},
		markChannelReadFn: func(_ context.Context, channelID, userID, messageID string) error {
			t.Fatalf("MarkChannelRead must not be called on message.send; got channelID=%q userID=%q messageID=%q", channelID, userID, messageID)
			return nil
		},
	}
	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	raw, _ := json.Marshal(map[string]interface{}{
		"channel_id":              "ch1",
		"ciphertext_by_recipient": map[string]string{"user2": "YWVz", "user3": "eHl6"},
	})
	h.Handle(sender, "message.send", raw)

	// Drain expected broadcasts; t.Fatalf fires immediately if MarkChannelRead is called.
	drainUntilType(t, recv, "message.new", time.Second)
	drainUntilType(t, sender, "message.new", time.Second)
}

func TestMessageHandler_HandleMLSCommit_ForbiddenWhenNotMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return false, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]interface{}{
		"channel_id":   "ch1",
		"commit_bytes": "Y29tbWl0",
		"group_info":   "Z3JvdXA=",
		"epoch":        int64(1),
	})
	h.Handle(c, "mls.commit", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct{ Type, Code string }
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "forbidden", out.Code)
}

func TestMessageHandler_HandleMLSCommit_BroadcastsToChannel(t *testing.T) {
	hub := NewHub()
	upsertCalled := false
	appendCalled := false
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	// Use function-field overrides for MLS group methods via embedded fields.
	// Since messageStoreMock has fixed stubs, we use a wrapper.
	_ = upsertCalled
	_ = appendCalled

	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	raw, _ := json.Marshal(map[string]interface{}{
		"channel_id":   "ch1",
		"commit_bytes": "Y29tbWl0",
		"group_info":   "Z3JvdXA=",
		"epoch":        int64(2),
	})
	h.Handle(sender, "mls.commit", raw)

	// receiver should get mls.commit broadcast
	msg := drainUntilType(t, recv, "mls.commit", time.Second)
	var out struct {
		Type           string `json:"type"`
		ChannelID      string `json:"channel_id"`
		Epoch          int64  `json:"epoch"`
		SenderID       string `json:"sender_id"`
		SenderDeviceID string `json:"sender_device_id"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "mls.commit", out.Type)
	assert.Equal(t, "ch1", out.ChannelID)
	assert.Equal(t, int64(2), out.Epoch)
	assert.Equal(t, "user1", out.SenderID)
	assert.Equal(t, "device-1", out.SenderDeviceID)
}

func TestMessageHandler_HandleMLSLeaveProposal_BroadcastsAddRequest(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	raw, _ := json.Marshal(map[string]interface{}{
		"channel_id":     "ch1",
		"proposal_bytes": "cHJvcG9zYWw=",
	})
	h.Handle(sender, "mls.leave_proposal", raw)

	// receiver should get mls.add_request broadcast
	msg := drainUntilType(t, recv, "mls.add_request", time.Second)
	var out struct {
		Type        string `json:"type"`
		ChannelID   string `json:"channel_id"`
		Action      string `json:"action"`
		RequesterID string `json:"requester_id"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "mls.add_request", out.Type)
	assert.Equal(t, "ch1", out.ChannelID)
	assert.Equal(t, "remove", out.Action)
	assert.Equal(t, "user1", out.RequesterID)
}

func TestMessageSizeLimit_RejectsOver8KiB(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	// Build a ciphertext exactly 1 byte over the 8 KiB limit (8193 bytes).
	oversized := make([]byte, 8*1024+1)
	for i := range oversized {
		oversized[i] = 0xAB
	}
	raw, _ := json.Marshal(map[string]string{
		"channel_id": "ch1",
		"ciphertext": base64.StdEncoding.EncodeToString(oversized),
	})
	h.Handle(c, "message.send", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "error", out.Type)
	assert.Equal(t, "bad_request", out.Code)
}

func TestMessageSizeLimit_AcceptsAtLimit(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
		insertMessageFn: func(ctx context.Context, channelID string, senderID *string, federatedSenderID *string, recipientID *string, ciphertext []byte) (*models.Message, error) {
			return &models.Message{
				ID:         "msg-ok",
				ChannelID:  channelID,
				SenderID:   senderID,
				Ciphertext: ciphertext,
				Timestamp:  time.Now(),
			}, nil
		},
	}
	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	// Build a ciphertext exactly at the 8 KiB limit (8192 bytes).
	atLimit := make([]byte, 8*1024)
	for i := range atLimit {
		atLimit[i] = 0xCD
	}
	raw, _ := json.Marshal(map[string]string{
		"channel_id": "ch1",
		"ciphertext": base64.StdEncoding.EncodeToString(atLimit),
	})
	h.Handle(sender, "message.send", raw)

	msg := drainUntilType(t, recv, "message.new", time.Second)
	var out struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "message.new", out.Type)
}

func TestMessageHandler_HandleTyping_BroadcastsToChannel(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return true, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	other := NewClient(nil, hub, "user2", "device-2", "", nil)
	hub.Register(other)
	hub.Subscribe(c, "ch1")
	hub.Subscribe(other, "ch1")
	defer func() {
		hub.Unregister(c)
		hub.Unregister(other)
		close(c.send)
		close(other.send)
	}()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "typing.start", raw)

	msg := drainUntilType(t, other, "typing.start", time.Second)
	var out struct {
		Type      string `json:"type"`
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "typing.start", out.Type)
	assert.Equal(t, "ch1", out.ChannelID)
	assert.Equal(t, "user1", out.UserID)
}

func TestMessageHandler_HandleMarkRead_ForbiddenWhenNotMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(_ context.Context, _, _ string) (bool, error) {
			return false, nil
		},
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "message_id": "msg1"})
	h.Handle(c, "message.mark_read", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "forbidden", out.Code)
}

func TestMessageHandler_HandleMarkRead_CallsMarkChannelRead(t *testing.T) {
	hub := NewHub()
	var calledWith [3]string
	store := &messageStoreMock{
		isChannelMemberFn: func(_ context.Context, _, _ string) (bool, error) {
			return true, nil
		},
		markChannelReadFn: func(_ context.Context, channelID, userID, messageID string) error {
			calledWith = [3]string{channelID, userID, messageID}
			return nil
		},
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user42", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "message_id": "msg99"})
	h.Handle(c, "message.mark_read", raw)

	// No error message should be sent.
	select {
	case msg := <-c.send:
		var out struct{ Type string }
		_ = json.Unmarshal(msg, &out)
		assert.NotEqual(t, "error", out.Type, "unexpected error message: %s", string(msg))
	default:
		// Nothing sent; correct.
	}
	assert.Equal(t, [3]string{"ch1", "user42", "msg99"}, calledWith)
}

func TestMessageHandler_HandleMarkRead_InternalOnMarkError(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(_ context.Context, _, _ string) (bool, error) {
			return true, nil
		},
		markChannelReadFn: func(_ context.Context, _, _, _ string) error {
			return errors.New("db error")
		},
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "message_id": "msg1"})
	h.Handle(c, "message.mark_read", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "internal", out.Code)
}

func TestMessageHandler_HandleMarkRead_BadRequestMissingFields(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", "device-1", "", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	// Payload missing message_id.
	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "message.mark_read", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "bad_request", out.Code)
}
