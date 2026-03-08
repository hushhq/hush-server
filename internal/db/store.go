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
	CreateUser(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error)
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	GetUserByID(ctx context.Context, id string) (*models.User, error)
	CreateSession(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*models.Session, error)
	DeleteSessionByID(ctx context.Context, sessionID string) error

	// Signal key methods
	UpsertIdentityKeys(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error
	InsertOneTimePreKeys(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error
	GetIdentityAndSignedPreKey(ctx context.Context, userID, deviceID string) (identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int, err error)
	ConsumeOneTimePreKey(ctx context.Context, userID, deviceID string) (keyID int, publicKey []byte, err error)
	CountUnusedOneTimePreKeys(ctx context.Context, userID, deviceID string) (int, error)
	ListDeviceIDsForUser(ctx context.Context, userID string) ([]string, error)
	UpsertDevice(ctx context.Context, userID, deviceID, label string) error

	// Message methods
	InsertMessage(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	IsChannelMember(ctx context.Context, channelID, userID string) (bool, error)

	// Instance config methods
	GetInstanceConfig(ctx context.Context) (*models.InstanceConfig, error)
	UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string, serverCreationPolicy *string) error
	SetInstanceOwner(ctx context.Context, userID string) (bool, error)
	GetUserRole(ctx context.Context, userID string) (string, error)
	UpdateUserRole(ctx context.Context, userID, role string) error
	ListMembers(ctx context.Context) ([]models.Member, error)

	// Channel operations
	CreateChannel(ctx context.Context, serverID, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	ListChannels(ctx context.Context, serverID string) ([]models.Channel, error)
	GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error)
	GetChannelByNameAndType(ctx context.Context, serverID, name, channelType string) (*models.Channel, error)
	DeleteChannel(ctx context.Context, channelID string) error
	MoveChannel(ctx context.Context, channelID string, parentID *string, position int) error

	// Server template
	UpdateServerTemplate(ctx context.Context, template json.RawMessage) error

	// Invite operations
	CreateInvite(ctx context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	GetInviteByCode(ctx context.Context, code string) (*models.InviteCode, error)
	ClaimInviteUse(ctx context.Context, code string) (bool, error)

	// Server operations
	CreateServer(ctx context.Context, name, ownerID string) (*models.Server, error)
	GetServerByID(ctx context.Context, serverID string) (*models.Server, error)
	ListServersForUser(ctx context.Context, userID string) ([]models.Server, error)
	DeleteServer(ctx context.Context, serverID string) error
	ListGuildBillingStats(ctx context.Context) ([]models.GuildBillingStats, error)

	// Server member operations
	AddServerMember(ctx context.Context, serverID, userID, role string) error
	RemoveServerMember(ctx context.Context, serverID, userID string) error
	GetServerMemberRole(ctx context.Context, serverID, userID string) (string, error)
	UpdateServerMemberRole(ctx context.Context, serverID, userID, role string) error
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
}

// InstanceAuditLogFilter filters instance audit log queries.
type InstanceAuditLogFilter struct {
	Action   string
	TargetID string
}
