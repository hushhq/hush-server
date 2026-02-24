package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPool_CreateUser_GetUserByID_Integration verifies the test DB pattern:
// migrations run, pool works, CreateUser and GetUserByID round-trip.
func TestPool_CreateUser_GetUserByID_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	username := "testuser_" + uuid.New().String()[:8]
	displayName := "Test User"
	hash := "bcryptplaceholder"
	user, err := pool.CreateUser(ctx, username, displayName, &hash)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.NotEmpty(t, user.ID)
	assert.Equal(t, username, user.Username)
	assert.Equal(t, displayName, user.DisplayName)

	found, err := pool.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, user.ID, found.ID)
	assert.Equal(t, username, found.Username)
}
