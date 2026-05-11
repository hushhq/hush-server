package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushhq/hush-server/internal/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- InstanceCache ----------

func TestInstanceCache_ZeroValue_ReturnsDefaults(t *testing.T) {
	cache := NewInstanceCache()
	name, iconURL, regMode, scp, vkrh, tURL, lPub, _, screenCap, maxAttachmentBytes := cache.snapshot()
	assert.Equal(t, "", name)
	assert.Nil(t, iconURL)
	assert.Equal(t, "", regMode)
	assert.Equal(t, "allowed", scp, "zero-value cache must default guild_discovery to 'allowed'")
	assert.Equal(t, voiceKeyRotationHoursDefault, vkrh, "zero-value cache must use default voice key rotation hours")
	assert.Nil(t, tURL, "zero-value cache must have nil transparencyURL")
	assert.Nil(t, lPub, "zero-value cache must have nil logPublicKey")
	assert.Equal(t, "1080p", screenCap)
	assert.Equal(t, int64(MaxAttachmentBytes), maxAttachmentBytes)
}

func TestInstanceCache_Set_ReflectsValues(t *testing.T) {
	cache := NewInstanceCache()
	icon := "https://example.com/icon.png"
	cache.Set("My Hush", &icon, "invite_only", "admin_only", 4, "open")

	name, iconURL, regMode, scp, vkrh, _, _, _, screenCap, _ := cache.snapshot()
	assert.Equal(t, "My Hush", name)
	require.NotNil(t, iconURL)
	assert.Equal(t, "https://example.com/icon.png", *iconURL)
	assert.Equal(t, "invite_only", regMode)
	assert.Equal(t, "admin_only", scp)
	assert.Equal(t, 4, vkrh)
	assert.Equal(t, "1080p", screenCap)
}

func TestInstanceCache_Set_NilIconURL(t *testing.T) {
	cache := NewInstanceCache()
	cache.Set("Test", nil, "open", "any_member", 2, "open")

	_, iconURL, _, _, _, _, _, _, _, _ := cache.snapshot()
	assert.Nil(t, iconURL)
}

// ---------- HandshakeHandler ----------

func TestHandshake_Returns200_NoAuth(t *testing.T) {
	cache := NewInstanceCache()
	cache.Set("Hush Instance", nil, "open", "any_member", 2, "open")
	handler := HandshakeHandler(cache, true)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	// No Authorization header - this endpoint is public
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandshake_ContainsAllRequiredFields(t *testing.T) {
	cache := NewInstanceCache()
	icon := "https://example.com/icon.png"
	cache.Set("My Instance", &icon, "invite_only", "admin_only", 2, "open")
	handler := HandshakeHandler(cache, true)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))

	// Version fields
	assert.Contains(t, resp, "server_version")
	assert.Contains(t, resp, "api_version")
	assert.Contains(t, resp, "min_client_version")
	assert.Contains(t, resp, "key_package_low_threshold")

	// Capabilities
	assert.Contains(t, resp, "capabilities")
	assert.Equal(t, "1080p", resp["screen_share_resolution_cap"])
	assert.Equal(t, float64(MaxAttachmentBytes), resp["max_attachment_bytes"])

	// Instance identity
	assert.Contains(t, resp, "name")
	assert.Contains(t, resp, "iconUrl")
	assert.Contains(t, resp, "registrationMode")
	assert.Contains(t, resp, "guild_discovery")

	// Voice MLS
	assert.Contains(t, resp, "voice_key_rotation_hours")
}

func TestHandshake_ServerVersionDefaultsDev(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "dev", resp.ServerVersion, "server_version defaults to 'dev' without ldflags")
	assert.Equal(t, version.ServerVersion, resp.ServerVersion)
}

func TestHandshake_KeyPackageLowThresholdIs10(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 10, resp.KeyPackageLowThreshold)
}

func TestHandshake_E2EEChatAlwaysTrue(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Capabilities["e2ee.chat"], "e2ee.chat must always be true")
}

func TestHandshake_E2EEMediaAlwaysTrue(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Capabilities["e2ee.media"], "e2ee.media must always be true")
}

func TestHandshake_VoiceChannels_TrueWhenEnabled(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, true) // voiceEnabled = true

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Capabilities["voice.channels"], "voice.channels must be true when voiceEnabled=true")
}

func TestHandshake_VoiceChannels_FalseWhenDisabled(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false) // voiceEnabled = false

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Capabilities["voice.channels"], "voice.channels must be false when voiceEnabled=false")
}

func TestHandshake_UninitializedCache_ReturnsZeroValues(t *testing.T) {
	cache := NewInstanceCache()
	// Do NOT call Set - test zero-value behavior

	handler := HandshakeHandler(cache, false)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "", resp.Name, "uninitialized cache: name must be empty string")
	assert.Nil(t, resp.IconURL, "uninitialized cache: iconUrl must be nil/omitted")
	assert.Equal(t, "", resp.RegistrationMode, "uninitialized cache: registrationMode must be empty")
}

func TestHandshake_InstanceIdentity_PopulatedFromCache(t *testing.T) {
	cache := NewInstanceCache()
	icon := "https://hush.example.com/logo.png"
	cache.Set("Hush Corp", &icon, "invite_only", "admin_only", 2, "open")

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "Hush Corp", resp.Name)
	require.NotNil(t, resp.IconURL)
	assert.Equal(t, "https://hush.example.com/logo.png", *resp.IconURL)
	assert.Equal(t, "invite_only", resp.RegistrationMode)
	// ServerCreationPolicy / guild_discovery is stored as guildDiscovery in the cache.
	assert.Equal(t, "admin_only", resp.GuildDiscovery)
}

func TestHandshake_VoiceKeyRotationHours_InResponse(t *testing.T) {
	cache := NewInstanceCache()
	cache.Set("Test", nil, "open", "any_member", 4, "open")

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 4, resp.VoiceKeyRotationHours, "voice_key_rotation_hours must reflect configured value")
}

func TestHandshake_VoiceKeyRotationHours_DefaultIs2(t *testing.T) {
	cache := NewInstanceCache()
	// Do NOT call Set - verify default is 2 hours

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, voiceKeyRotationHoursDefault, resp.VoiceKeyRotationHours, "default voice_key_rotation_hours must be 2")
}

func TestHandshake_ScreenShareResolutionCap(t *testing.T) {
	cache := NewInstanceCache()
	cache.Set("Test Instance", nil, "open", "allowed", 2, "open", "720p")

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "720p", body["screen_share_resolution_cap"])
}

func TestHandshake_MaxAttachmentBytes(t *testing.T) {
	cache := NewInstanceCache()
	cache.SetAttachmentPolicy(4 * 1024 * 1024)

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, float64(4*1024*1024), body["max_attachment_bytes"])
}

// TestHandshake_ServerCreationPolicy verifies that a non-default serverCreationPolicy
// value set via cache.Set round-trips through the handshake response JSON.
func TestHandshake_ServerCreationPolicy(t *testing.T) {
	cache := NewInstanceCache()
	cache.Set("Test Instance", nil, "open", "allowed", 2, "disabled")

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "disabled", body["server_creation_policy"], "server_creation_policy must reflect the cached value")
}

// TestHandshake_CurrentMLSCiphersuite_XWing verifies the handshake advertises the
// active OpenMLS ciphersuite so clients can refuse to upload state created under a
// different suite. Today that value is MLS_256_XWING_CHACHA20POLY1305_SHA256_Ed25519
// (codepoint 0x004D = decimal 77). The constant must not drift away from this
// without an intentional protocol-epoch migration.
func TestHandshake_CurrentMLSCiphersuite_XWing(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp handshakeResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 77, resp.CurrentMLSCiphersuite,
		"current_mls_ciphersuite must equal OpenMLS codepoint 0x004D (77) for X-Wing")
	assert.Equal(t, version.CurrentMLSCiphersuite, resp.CurrentMLSCiphersuite,
		"current_mls_ciphersuite must match version.CurrentMLSCiphersuite")
}

// TestHandshake_ServerCreationPolicy_DefaultIsOpen verifies the handshake response
// includes server_creation_policy="open" when the cache uses its default value.
func TestHandshake_ServerCreationPolicy_DefaultIsOpen(t *testing.T) {
	cache := NewInstanceCache()
	// Do NOT call Set - verify default is "open"

	handler := HandshakeHandler(cache, true)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "open", body["server_creation_policy"], "default server_creation_policy must be 'open'")
}
