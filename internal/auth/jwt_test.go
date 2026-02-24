package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignJWT_ValidInput_ReturnsToken(t *testing.T) {
	token, err := SignJWT("user-1", "sess-1", "secret", time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestValidateJWT_ValidToken_ReturnsUserIDAndSessionID(t *testing.T) {
	secret := "test-secret"
	wantUser := "user-42"
	wantSession := "sess-99"

	token, err := SignJWT(wantUser, wantSession, secret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	gotUser, gotSession, err := ValidateJWT(token, secret)

	require.NoError(t, err)
	assert.Equal(t, wantUser, gotUser)
	assert.Equal(t, wantSession, gotSession)
}

func TestValidateJWT_ExpiredToken_ReturnsError(t *testing.T) {
	token, err := SignJWT("user-1", "sess-1", "secret", time.Now().Add(-time.Hour))
	require.NoError(t, err)

	_, _, err = ValidateJWT(token, "secret")

	assert.Error(t, err)
}

func TestValidateJWT_WrongSecret_ReturnsError(t *testing.T) {
	token, err := SignJWT("user-1", "sess-1", "sign-secret", time.Now().Add(time.Hour))
	require.NoError(t, err)

	_, _, err = ValidateJWT(token, "wrong-secret")

	assert.Error(t, err)
}

func TestValidateJWT_MalformedToken_ReturnsError(t *testing.T) {
	_, _, err := ValidateJWT("not.a.jwt.at.all", "secret")

	assert.Error(t, err)
}

func TestTokenHash_DeterministicOutput(t *testing.T) {
	token := "some-jwt-token-string"

	hash1 := TokenHash(token)
	hash2 := TokenHash(token)

	assert.Equal(t, hash1, hash2)
}

func TestTokenHash_DifferentInputs_DifferentHashes(t *testing.T) {
	hash1 := TokenHash("token-a")
	hash2 := TokenHash("token-b")

	assert.NotEqual(t, hash1, hash2)
}
