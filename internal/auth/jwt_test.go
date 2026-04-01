package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignJWT_ValidInput_ReturnsToken(t *testing.T) {
	token, err := SignJWT("user-1", "sess-1", "device-1", "secret", time.Now().Add(time.Hour))

	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestValidateJWT_ValidToken_ReturnsUserIDAndSessionID(t *testing.T) {
	secret := "test-secret"
	wantUser := "user-42"
	wantSession := "sess-99"
	wantDevice := "device-7"

	token, err := SignJWT(wantUser, wantSession, wantDevice, secret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	gotUser, gotSession, gotDevice, isGuest, isFederated, federatedID, err := ValidateJWT(token, secret)

	require.NoError(t, err)
	assert.Equal(t, wantUser, gotUser)
	assert.Equal(t, wantSession, gotSession)
	assert.Equal(t, wantDevice, gotDevice)
	assert.False(t, isGuest)
	assert.False(t, isFederated)
	assert.Empty(t, federatedID)
}

func TestValidateJWT_GuestToken_ReturnsIsGuestTrue(t *testing.T) {
	secret := "test-secret"
	guestID := "guest-abc"
	sessID := "sess-guest-1"

	token, err := SignGuestJWT(guestID, sessID, secret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	gotUser, gotSession, gotDevice, isGuest, isFederated, federatedID, err := ValidateJWT(token, secret)

	require.NoError(t, err)
	assert.Equal(t, guestID, gotUser)
	assert.Equal(t, sessID, gotSession)
	assert.Empty(t, gotDevice)
	assert.True(t, isGuest)
	assert.False(t, isFederated)
	assert.Empty(t, federatedID)
}

func TestValidateJWT_ExpiredToken_ReturnsError(t *testing.T) {
	token, err := SignJWT("user-1", "sess-1", "device-1", "secret", time.Now().Add(-time.Hour))
	require.NoError(t, err)

	_, _, _, _, _, _, err = ValidateJWT(token, "secret")

	assert.Error(t, err)
}

func TestValidateJWT_WrongSecret_ReturnsError(t *testing.T) {
	token, err := SignJWT("user-1", "sess-1", "device-1", "sign-secret", time.Now().Add(time.Hour))
	require.NoError(t, err)

	_, _, _, _, _, _, err = ValidateJWT(token, "wrong-secret")

	assert.Error(t, err)
}

func TestValidateJWT_MalformedToken_ReturnsError(t *testing.T) {
	_, _, _, _, _, _, err := ValidateJWT("not.a.jwt.at.all", "secret")

	assert.Error(t, err)
}

func TestSignFederatedJWT_ValidInput_ReturnsFederatedClaims(t *testing.T) {
	secret := "test-secret"
	fedID := "fid-abc"
	sessID := "sess-fed-1"

	token, err := SignFederatedJWT(fedID, sessID, secret, time.Now().Add(time.Hour))
	require.NoError(t, err)

	gotUser, gotSession, gotDevice, isGuest, isFederated, federatedID, err := ValidateJWT(token, secret)

	require.NoError(t, err)
	assert.Equal(t, fedID, gotUser)
	assert.Equal(t, sessID, gotSession)
	assert.Empty(t, gotDevice)
	assert.False(t, isGuest)
	assert.True(t, isFederated)
	assert.Equal(t, fedID, federatedID)
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
