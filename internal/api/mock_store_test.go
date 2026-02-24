package api

import (
	"context"
	"time"

	"hush.app/server/internal/models"
)

// mockStore implements db.Store with function fields for per-test customization.
type mockStore struct {
	createUserFn            func(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error)
	getUserByUsernameFn     func(ctx context.Context, username string) (*models.User, error)
	getUserByIDFn           func(ctx context.Context, id string) (*models.User, error)
	createSessionFn         func(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error)
	getSessionByTokenHashFn func(ctx context.Context, tokenHash string) (*models.Session, error)
	deleteSessionByIDFn     func(ctx context.Context, sessionID string) error
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
