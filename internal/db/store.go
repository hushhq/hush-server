package db

import (
	"context"
	"time"

	"hush.app/server/internal/models"
)

// Store defines the database operations used by the API and WebSocket layers.
// *Pool satisfies this interface. Use for dependency injection in tests.
type Store interface {
	CreateUser(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error)
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	GetUserByID(ctx context.Context, id string) (*models.User, error)
	CreateSession(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*models.Session, error)
	DeleteSessionByID(ctx context.Context, sessionID string) error

	UpsertIdentityKeys(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error
	InsertOneTimePreKeys(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error
	GetIdentityAndSignedPreKey(ctx context.Context, userID, deviceID string) (identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int, err error)
	ConsumeOneTimePreKey(ctx context.Context, userID, deviceID string) (keyID int, publicKey []byte, err error)
	CountUnusedOneTimePreKeys(ctx context.Context, userID, deviceID string) (int, error)
	ListDeviceIDsForUser(ctx context.Context, userID string) ([]string, error)
	UpsertDevice(ctx context.Context, userID, deviceID, label string) error

	InsertMessage(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	IsChannelMember(ctx context.Context, channelID, userID string) (bool, error)

	// Server operations
	CreateServerWithOwner(ctx context.Context, name string, iconURL *string, ownerID string) (*models.Server, error)
	GetServerByID(ctx context.Context, serverID string) (*models.Server, error)
	ListServersForUser(ctx context.Context, userID string) ([]models.ServerWithRole, error)
	UpdateServer(ctx context.Context, serverID string, name *string, iconURL *string) error
	DeleteServer(ctx context.Context, serverID string) error
	AddServerMember(ctx context.Context, serverID, userID, role string) error
	RemoveServerMember(ctx context.Context, serverID, userID string) error
	GetServerMember(ctx context.Context, serverID, userID string) (*models.ServerMember, error)
	ListServerMembers(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error)
	TransferServerOwnership(ctx context.Context, serverID, newOwnerID string) error
	UpdateServerMemberRole(ctx context.Context, serverID, userID, role string) error
	CountServerMembers(ctx context.Context, serverID string) (int, error)
	GetNextOwnerCandidate(ctx context.Context, serverID, excludeUserID string) (*models.ServerMember, error)

	// Channel operations
	CreateChannel(ctx context.Context, serverID, name, channelType string, voiceMode *string, parentID *string, position int) (*models.Channel, error)
	ListChannels(ctx context.Context, serverID string) ([]models.Channel, error)
	GetChannelByID(ctx context.Context, channelID string) (*models.Channel, error)
	DeleteChannel(ctx context.Context, channelID string) error
	GetServerIDForChannel(ctx context.Context, channelID string) (string, error)

	// Invite operations
	GetInviteByCode(ctx context.Context, code string) (*models.InviteCode, error)
	ClaimInviteUse(ctx context.Context, code string) (bool, error)
}
