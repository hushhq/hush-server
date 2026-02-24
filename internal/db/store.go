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

	InsertMessage(ctx context.Context, channelID, senderID string, ciphertext []byte) (*models.Message, error)
	GetMessages(ctx context.Context, channelID string, before time.Time, limit int) ([]models.Message, error)
	IsChannelMember(ctx context.Context, channelID, userID string) (bool, error)
}
