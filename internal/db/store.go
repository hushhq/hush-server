package db

import (
	"context"
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
	UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string) error
	SetInstanceOwner(ctx context.Context, userID string) (bool, error)
	GetUserRole(ctx context.Context, userID string) (string, error)
	UpdateUserRole(ctx context.Context, userID, role string) error
	ListMembers(ctx context.Context) ([]models.Member, error)

	// Channel operations
	CreateChannel(ctx context.Context, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	ListChannels(ctx context.Context) ([]models.Channel, error)
	GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error)
	DeleteChannel(ctx context.Context, channelID string) error
	MoveChannel(ctx context.Context, channelID string, parentID *string, position int) error

	// Invite operations
	CreateInvite(ctx context.Context, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error)
	GetInviteByCode(ctx context.Context, code string) (*models.InviteCode, error)
	ClaimInviteUse(ctx context.Context, code string) (bool, error)

	// Moderation — bans
	InsertBan(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.Ban, error)
	GetActiveBan(ctx context.Context, userID string) (*models.Ban, error)
	LiftBan(ctx context.Context, banID, liftedByID string) error

	// Moderation — mutes
	InsertMute(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.Mute, error)
	GetActiveMute(ctx context.Context, userID string) (*models.Mute, error)
	LiftMute(ctx context.Context, muteID, liftedByID string) error

	// Moderation — audit log
	InsertAuditLog(ctx context.Context, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error
	ListAuditLog(ctx context.Context, limit, offset int) ([]models.AuditLogEntry, error)

	// Moderation — messages
	GetMessageByID(ctx context.Context, messageID string) (*models.Message, error)
	DeleteMessage(ctx context.Context, messageID string) error

	// Moderation — sessions
	DeleteSessionsByUserID(ctx context.Context, userID string) error
}
