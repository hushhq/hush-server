package db

import (
	"context"
	"encoding/json"
	"time"

	"hush.app/server/internal/models"
)

// Store defines the database operations used by the API and WebSocket layers.
// *Pool satisfies this interface. Use for dependency injection in tests.
type Store interface {
	// User/session methods

	// CreateUserWithPublicKey inserts a user with a BIP39 Ed25519 root public key
	// instead of a password. Returns the created user with a server-assigned ID.
	CreateUserWithPublicKey(ctx context.Context, username, displayName string, publicKey []byte) (*models.User, error)
	// GetUserByPublicKey returns the user whose root_public_key matches the
	// given 32-byte Ed25519 public key, or sql.ErrNoRows if not found.
	GetUserByPublicKey(ctx context.Context, publicKey []byte) (*models.User, error)
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	GetUserByID(ctx context.Context, id string) (*models.User, error)

	// BIP39 auth nonce methods
	// InsertAuthNonce stores a challenge nonce associated with a public key.
	InsertAuthNonce(ctx context.Context, nonce string, publicKey []byte, expiresAt time.Time) error
	// ConsumeAuthNonce atomically deletes the nonce (if present and unexpired)
	// and returns the associated public key. Returns sql.ErrNoRows if absent or expired.
	ConsumeAuthNonce(ctx context.Context, nonce string) (publicKey []byte, err error)
	// DeleteAuthNonce removes a stored nonce regardless of expiry.
	// Used to invalidate companion link records after the first scan/resolve.
	DeleteAuthNonce(ctx context.Context, nonce string) error
	// PurgeExpiredNonces deletes all auth_nonces where expires_at < now() and
	// returns the number of rows deleted.
	PurgeExpiredNonces(ctx context.Context) (int64, error)

	// Device key methods
	// InsertDeviceKey stores a certified device public key for a user.
	// certificate may be nil for the first (root) device.
	InsertDeviceKey(ctx context.Context, userID, deviceID, label string, devicePublicKey, certificate []byte) error
	// ListDeviceKeys returns all device keys belonging to a user.
	ListDeviceKeys(ctx context.Context, userID string) ([]models.DeviceKey, error)
	// RevokeDeviceKey deletes a specific device key. No-op if not found.
	RevokeDeviceKey(ctx context.Context, userID, deviceID string) error
	// RevokeAllDeviceKeys deletes every device key for a user (used on account wipe).
	RevokeAllDeviceKeys(ctx context.Context, userID string) error
	// UpdateDeviceLastSeen sets last_seen = now() for the given device.
	UpdateDeviceLastSeen(ctx context.Context, userID, deviceID string) error
	CreateSession(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*models.Session, error)
	DeleteSessionByID(ctx context.Context, sessionID string) error

	// MLS credential methods
	UpsertMLSCredential(ctx context.Context, userID, deviceID string, credentialBytes, signingPublicKey []byte, identityVersion int) error
	GetMLSCredential(ctx context.Context, userID, deviceID string) (credentialBytes, signingPublicKey []byte, identityVersion int, err error)

	// MLS key package methods
	InsertMLSKeyPackages(ctx context.Context, userID, deviceID string, packages [][]byte, expiresAt time.Time) error
	InsertMLSLastResortKeyPackage(ctx context.Context, userID, deviceID string, keyPackageBytes []byte) error
	ConsumeMLSKeyPackage(ctx context.Context, userID, deviceID string) (keyPackageBytes []byte, err error)
	CountUnusedMLSKeyPackages(ctx context.Context, userID, deviceID string) (int, error)
	PurgeExpiredMLSKeyPackages(ctx context.Context) (int64, error)

	// Device enumeration (now backed by mls_credentials)
	ListDeviceIDsForUser(ctx context.Context, userID string) ([]string, error)
	UpsertDevice(ctx context.Context, userID, deviceID, label string) error

	// Message methods
	InsertMessage(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	IsChannelMember(ctx context.Context, channelID, userID string) (bool, error)

	// Instance config methods
	GetInstanceConfig(ctx context.Context) (*models.InstanceConfig, error)
	// UpdateInstanceConfig updates only the non-nil fields. serverCreationPolicy must be
	// one of "open", "paid", or "disabled" when non-nil.
	UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string, guildDiscovery *string, serverCreationPolicy *string) error
	GetUserRole(ctx context.Context, userID string) (string, error)
	UpdateUserRole(ctx context.Context, userID, role string) error
	ListMembers(ctx context.Context) ([]models.Member, error)

	// Channel operations
	// CreateChannel uses encryptedMetadata instead of a plaintext name.
	CreateChannel(ctx context.Context, serverID string, encryptedMetadata []byte, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	ListChannels(ctx context.Context, serverID string) ([]models.Channel, error)
	GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error)
	// GetChannelByTypeAndPosition replaces GetChannelByNameAndType (no name column).
	GetChannelByTypeAndPosition(ctx context.Context, serverID, channelType string, position int) (*models.Channel, error)
	DeleteChannel(ctx context.Context, channelID string) error
	MoveChannel(ctx context.Context, channelID string, parentID *string, position int) error

	// Server templates
	ListServerTemplates(ctx context.Context) ([]models.ServerTemplate, error)
	GetServerTemplateByID(ctx context.Context, id string) (*models.ServerTemplate, error)
	GetDefaultServerTemplate(ctx context.Context) (*models.ServerTemplate, error)
	CreateServerTemplate(ctx context.Context, name string, channels json.RawMessage, isDefault bool) (*models.ServerTemplate, error)
	UpdateServerTemplate(ctx context.Context, id string, name string, channels json.RawMessage, isDefault bool) error
	DeleteServerTemplate(ctx context.Context, id string) error

	// Invite operations
	CreateInvite(ctx context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	GetInviteByCode(ctx context.Context, code string) (*models.InviteCode, error)
	ClaimInviteUse(ctx context.Context, code string) (bool, error)

	// Server operations
	// CreateServer accepts encryptedMetadata (may be nil for two-step creation flow).
	CreateServer(ctx context.Context, encryptedMetadata []byte) (*models.Server, error)
	// UpdateServerEncryptedMetadata updates only the encrypted_metadata blob.
	// Used in the two-step guild creation flow and after MLS epoch advances.
	// Returns sql.ErrNoRows if no server with that ID exists.
	UpdateServerEncryptedMetadata(ctx context.Context, serverID string, encryptedMetadata []byte) error
	GetServerByID(ctx context.Context, serverID string) (*models.Server, error)
	ListServersForUser(ctx context.Context, userID string) ([]models.Server, error)
	DeleteServer(ctx context.Context, serverID string) error
	ListGuildBillingStats(ctx context.Context) ([]models.GuildBillingStats, error)

	// Server member operations
	// AddServerMember uses permissionLevel int instead of role string.
	AddServerMember(ctx context.Context, serverID, userID string, permissionLevel int) error
	RemoveServerMember(ctx context.Context, serverID, userID string) error
	// GetServerMemberLevel returns the permission_level int for a guild member.
	GetServerMemberLevel(ctx context.Context, serverID, userID string) (int, error)
	// UpdateServerMemberLevel sets a new permission level for the given member.
	UpdateServerMemberLevel(ctx context.Context, serverID, userID string, permissionLevel int) error
	ListServerMembers(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error)

	// Moderation — bans
	InsertBan(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Ban, error)
	GetActiveBan(ctx context.Context, serverID, userID string) (*models.Ban, error)
	LiftBan(ctx context.Context, banID, liftedByID string) error
	ListActiveBans(ctx context.Context, serverID string) ([]models.Ban, error)

	// Moderation — mutes
	InsertMute(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Mute, error)
	GetActiveMute(ctx context.Context, serverID, userID string) (*models.Mute, error)
	LiftMute(ctx context.Context, muteID, liftedByID string) error
	ListActiveMutes(ctx context.Context, serverID string) ([]models.Mute, error)

	// Moderation — audit log
	InsertAuditLog(ctx context.Context, serverID, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error
	ListAuditLog(ctx context.Context, serverID string, limit, offset int, filter *AuditLogFilter) ([]models.AuditLogEntry, error)

	// Moderation — messages
	GetMessageByID(ctx context.Context, messageID string) (*models.Message, error)
	DeleteMessage(ctx context.Context, messageID string) error

	// Moderation — sessions
	DeleteSessionsByUserID(ctx context.Context, userID string) error

	// Instance bans
	InsertInstanceBan(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.InstanceBan, error)
	GetActiveInstanceBan(ctx context.Context, userID string) (*models.InstanceBan, error)
	LiftInstanceBan(ctx context.Context, banID, liftedByID string) error

	// Instance audit log
	InsertInstanceAuditLog(ctx context.Context, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error
	ListInstanceAuditLog(ctx context.Context, limit, offset int, filter *InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error)

	// User search (admin)
	SearchUsers(ctx context.Context, query string, limit int) ([]models.UserSearchResult, error)

	// System messages
	InsertSystemMessage(ctx context.Context, serverID, eventType, actorID string, targetID *string, reason string, metadata map[string]interface{}) (*models.SystemMessage, error)
	ListSystemMessages(ctx context.Context, serverID string, before time.Time, limit int) ([]models.SystemMessage, error)
	PurgeExpiredSystemMessages(ctx context.Context, retentionDays int) (int64, error)
	GetSystemMessageRetentionDays(ctx context.Context) (*int, error)

	// MLS group methods
	// groupType is "text" or "voice" — each channel can have one group of each type.
	UpsertMLSGroupInfo(ctx context.Context, channelID string, groupType string, groupInfoBytes []byte, epoch int64) error
	GetMLSGroupInfo(ctx context.Context, channelID string, groupType string) (groupInfoBytes []byte, epoch int64, err error)
	DeleteMLSGroupInfo(ctx context.Context, channelID string, groupType string) error
	AppendMLSCommit(ctx context.Context, channelID string, epoch int64, commitBytes []byte, senderID string) error
	GetMLSCommitsSinceEpoch(ctx context.Context, channelID string, sinceEpoch int64, limit int) ([]MLSCommitRow, error)
	PurgeOldMLSCommits(ctx context.Context, maxPerChannel int) (int64, error)
	StorePendingWelcome(ctx context.Context, channelID, recipientUserID, senderID string, welcomeBytes []byte, epoch int64) error
	GetPendingWelcomes(ctx context.Context, recipientUserID string) ([]PendingWelcomeRow, error)
	DeletePendingWelcome(ctx context.Context, welcomeID string) error
	// GetVoiceKeyRotationHours returns the configured voice group key rotation interval in hours.
	GetVoiceKeyRotationHours(ctx context.Context) (int, error)

	// MLS guild metadata group methods (server_id scoped, group_type = 'metadata').
	UpsertMLSGuildMetadataGroupInfo(ctx context.Context, serverID string, groupInfoBytes []byte, epoch int64) error
	GetMLSGuildMetadataGroupInfo(ctx context.Context, serverID string) (groupInfoBytes []byte, epoch int64, err error)
	DeleteMLSGuildMetadataGroupInfo(ctx context.Context, serverID string) error

	// Guild metrics increment methods.
	// IncrementGuildMessageCount increments the message_count and updates last_active_at for
	// the guild that owns the given channel.
	IncrementGuildMessageCount(ctx context.Context, channelID string) error
	// IncrementGuildMemberCount adjusts member_count by delta (+1 on join, -1 on leave).
	IncrementGuildMemberCount(ctx context.Context, serverID string, delta int) error
	// UpdateGuildChannelCounts recalculates text_channel_count and voice_channel_count for the guild.
	UpdateGuildChannelCounts(ctx context.Context, serverID string) error

	// DM and discovery methods (migration 000021).

	// FindDMGuild returns the DM guild for the given user pair, or sql.ErrNoRows
	// when no DM exists yet. User IDs are canonically ordered internally.
	FindDMGuild(ctx context.Context, userAID, userBID string) (*models.Server, error)
	// CreateDMGuild creates a DM guild for the two users in a single transaction.
	// Inserts the servers row (is_dm=true), dm_pairs entry, both server_members
	// (PermissionLevelOwner), and one text channel. Returns the new server.
	CreateDMGuild(ctx context.Context, userAID, userBID string) (*models.Server, *models.Channel, error)
	// DiscoverGuilds returns publicly discoverable guilds filtered by category,
	// search query, and sort order. Returns results and total count for pagination.
	DiscoverGuilds(ctx context.Context, category, search, sort string, page, pageSize int) ([]models.DiscoverGuild, int, error)
	// SearchUsersPublic returns users matching the query on username or displayName.
	// Only id, username, and displayName are returned — no ban/role info.
	SearchUsersPublic(ctx context.Context, query string, limit int) ([]models.UserSearchPublicResult, error)

	// Transparency log methods (migration 000019).
	// InsertTransparencyLogEntry persists a signed log entry after it is appended
	// to the Merkle tree. All byte slices are mandatory.
	InsertTransparencyLogEntry(ctx context.Context, leafIndex uint64, operation string, userPubKey, subjectKey, entryCBOR, leafHash, userSig, logSig []byte) error
	// GetTransparencyLogEntriesByPubKey returns all log entries for a public key,
	// ordered by leaf_index ASC. Returns an empty slice when no entries exist.
	GetTransparencyLogEntriesByPubKey(ctx context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error)
	// GetLatestTransparencyTreeHead returns the highest tree_size row, or nil
	// when the log is empty (no error).
	GetLatestTransparencyTreeHead(ctx context.Context) (*models.TransparencyTreeHead, error)
	// InsertTransparencyTreeHead persists the Merkle tree state after each append.
	InsertTransparencyTreeHead(ctx context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error
}

// InstanceAuditLogFilter filters instance audit log queries.
type InstanceAuditLogFilter struct {
	Action   string
	TargetID string
}
