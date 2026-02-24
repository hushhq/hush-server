package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword_ValidPassword_ReturnsHash(t *testing.T) {
	hash, err := HashPassword("correcthorsebatterystaple")

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestComparePassword_CorrectPassword_ReturnsTrue(t *testing.T) {
	password := "my-secure-password"

	hash, err := HashPassword(password)
	require.NoError(t, err)

	assert.True(t, ComparePassword(hash, password))
}

func TestComparePassword_WrongPassword_ReturnsFalse(t *testing.T) {
	hash, err := HashPassword("real-password")
	require.NoError(t, err)

	assert.False(t, ComparePassword(hash, "wrong-password"))
}

func TestHashPassword_DifferentCallsSamePassword_DifferentHashes(t *testing.T) {
	password := "same-password"

	hash1, err := HashPassword(password)
	require.NoError(t, err)

	hash2, err := HashPassword(password)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2)
}
