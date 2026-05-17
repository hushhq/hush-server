package livekit

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeJWTClaims returns the JSON-decoded payload (middle segment) of
// a LiveKit JWT. We decode by hand here rather than going through the
// LiveKit verifier so the test does not depend on the verifier's own
// internals when asserting that arbitrary claims (like `metadata`)
// reach the wire correctly.
func decodeJWTClaims(t *testing.T, token string) map[string]interface{} {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT must have 3 segments")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

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

// ---------- GenerateAccessToken with metadata ----------

func TestGenerateAccessToken_EmbedsMetadataClaim(t *testing.T) {
	// Voice-MLS eviction depends on the metadata claim carrying a
	// stable `userId:deviceId` so remote clients can resolve the
	// departed device's MLS leaf without trusting LiveKit's bare
	// participant identity. Verify the metadata round-trips through
	// the JWT verbatim.
	metadata := `{"userId":"u-1","deviceId":"d-1","mlsIdentity":"u-1:d-1"}`
	token, err := GenerateAccessToken(TokenOptions{
		APIKey:          "api-key",
		APISecret:       "api-secret",
		Identity:        "u-1",
		RoomName:        "channel-x",
		ParticipantName: "Alice",
		Metadata:        metadata,
		ValidFor:        time.Hour,
	})
	require.NoError(t, err)

	claims := decodeJWTClaims(t, token)
	assert.Equal(t, metadata, claims["metadata"], "metadata claim must round-trip through the JWT")
}

func TestGenerateAccessToken_NoMetadata_OmitsClaim(t *testing.T) {
	token, err := GenerateAccessToken(TokenOptions{
		APIKey:          "api-key",
		APISecret:       "api-secret",
		Identity:        "u-1",
		RoomName:        "channel-x",
		ParticipantName: "Alice",
		ValidFor:        time.Hour,
	})
	require.NoError(t, err)

	claims := decodeJWTClaims(t, token)
	// LiveKit's auth lib omits empty metadata; the claim should either
	// be absent or an empty string. Either is fine — what we must NOT
	// have is stale fake metadata.
	if md, ok := claims["metadata"]; ok {
		assert.Equal(t, "", md)
	}
}

func TestGenerateAccessToken_EmptyAPIKey_ReturnsError(t *testing.T) {
	_, err := GenerateAccessToken(TokenOptions{
		APISecret: "api-secret",
		Identity:  "u-1",
		RoomName:  "channel-x",
		ValidFor:  time.Hour,
	})
	assert.Error(t, err)
}
