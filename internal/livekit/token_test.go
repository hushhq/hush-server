package livekit

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateToken_ValidInput_ReturnsJWT(t *testing.T) {
	token, err := GenerateToken("api-key", "api-secret", "user-1", "room-a", "Alice", time.Hour)

	require.NoError(t, err)
	assert.NotEmpty(t, token)

	parts := strings.Split(token, ".")
	assert.Len(t, parts, 3, "JWT must have 3 dot-separated segments")
}

func TestGenerateToken_EmptyAPIKey_ReturnsError(t *testing.T) {
	_, err := GenerateToken("", "api-secret", "user-1", "room-a", "Alice", time.Hour)

	assert.Error(t, err)
}

func TestGenerateToken_EmptyAPISecret_ReturnsError(t *testing.T) {
	_, err := GenerateToken("api-key", "", "user-1", "room-a", "Alice", time.Hour)

	assert.Error(t, err)
}

func TestGenerateToken_BothEmpty_ReturnsError(t *testing.T) {
	_, err := GenerateToken("", "", "user-1", "room-a", "Alice", time.Hour)

	assert.Error(t, err)
}

func TestGenerateToken_DifferentRooms_DifferentTokens(t *testing.T) {
	tokenA, err := GenerateToken("api-key", "api-secret", "user-1", "room-a", "Alice", time.Hour)
	require.NoError(t, err)

	tokenB, err := GenerateToken("api-key", "api-secret", "user-1", "room-b", "Alice", time.Hour)
	require.NoError(t, err)

	assert.NotEqual(t, tokenA, tokenB)
}

func TestGenerateToken_DifferentIdentities_DifferentTokens(t *testing.T) {
	tokenA, err := GenerateToken("api-key", "api-secret", "user-1", "room-a", "Alice", time.Hour)
	require.NoError(t, err)

	tokenB, err := GenerateToken("api-key", "api-secret", "user-2", "room-a", "Bob", time.Hour)
	require.NoError(t, err)

	assert.NotEqual(t, tokenA, tokenB)
}
