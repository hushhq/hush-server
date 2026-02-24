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
}
