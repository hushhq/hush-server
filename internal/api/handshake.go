package api

import (
	"net/http"
	"sync"

	"github.com/hushhq/hush-server/internal/version"
)

// InstanceCache holds a snapshot of the instance_config row in memory so the
// handshake handler never hits the database. It is populated on server startup
// and refreshed whenever the instance config is updated via PUT /api/instance.
type InstanceCache struct {
	mu                       sync.RWMutex
	name                     string
	iconURL                  *string
	registrationMode         string
	guildDiscovery           string
	voiceKeyRotationHours    int
	serverCreationPolicy     string
	screenShareResolutionCap string
	maxAttachmentBytes       int64
	transparencyURL          *string
	logPublicKey             *string
}

// voiceKeyRotationHoursDefault is the default voice group key rotation interval.
// Matches the instance_config column default (2 hours).
const voiceKeyRotationHoursDefault = 2

// NewInstanceCache creates an empty cache. Zero values are safe: name="" -
// representing a fresh instance before first-user setup.
// voiceKeyRotationHours defaults to voiceKeyRotationHoursDefault.
// guildDiscovery defaults to "allowed".
// serverCreationPolicy defaults to "open".
func NewInstanceCache() *InstanceCache {
	return &InstanceCache{
		voiceKeyRotationHours:    voiceKeyRotationHoursDefault,
		guildDiscovery:           "allowed",
		serverCreationPolicy:     "open",
		screenShareResolutionCap: "1080p",
		maxAttachmentBytes:       MaxAttachmentBytes,
	}
}

// Set updates the instance configuration fields under a write lock. Called on
// startup (from GetInstanceConfig) and after updateConfig writes to the database.
//
// serverCreationPolicy must be one of "open", "paid", or "disabled"; defaults to "open"
// when the empty string is passed.
//
// This method does not touch transparency fields (transparencyURL, logPublicKey).
// Use SetTransparencyInfo to update those separately after the log signer is loaded.
func (c *InstanceCache) Set(name string, iconURL *string, regMode string, guildDiscovery string, voiceKeyRotationHours int, serverCreationPolicy string, screenShareResolutionCap ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.name = name
	if iconURL != nil {
		v := *iconURL
		c.iconURL = &v
	} else {
		c.iconURL = nil
	}
	c.registrationMode = regMode
	c.guildDiscovery = guildDiscovery
	if voiceKeyRotationHours > 0 {
		c.voiceKeyRotationHours = voiceKeyRotationHours
	} else {
		c.voiceKeyRotationHours = voiceKeyRotationHoursDefault
	}
	if serverCreationPolicy != "" {
		c.serverCreationPolicy = serverCreationPolicy
	} else {
		c.serverCreationPolicy = "open"
	}
	if len(screenShareResolutionCap) > 0 && screenShareResolutionCap[0] != "" {
		c.screenShareResolutionCap = screenShareResolutionCap[0]
	} else {
		c.screenShareResolutionCap = "1080p"
	}
}

// SetAttachmentPolicy updates cache fields that affect attachment UX and
// upload validation. Called with instance_config on startup and after admin
// config writes.
func (c *InstanceCache) SetAttachmentPolicy(maxAttachmentBytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if maxAttachmentBytes > 0 {
		c.maxAttachmentBytes = maxAttachmentBytes
	} else {
		c.maxAttachmentBytes = MaxAttachmentBytes
	}
}

// SetTransparencyInfo stores the transparency log URL and log signer public key
// in the cache. Called once at startup after the TransparencyService is initialized.
// Neither field is updated by the admin config endpoint - they change only on restart.
func (c *InstanceCache) SetTransparencyInfo(transparencyURL *string, logPublicKey *string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if transparencyURL != nil {
		v := *transparencyURL
		c.transparencyURL = &v
	} else {
		c.transparencyURL = nil
	}
	if logPublicKey != nil {
		v := *logPublicKey
		c.logPublicKey = &v
	} else {
		c.logPublicKey = nil
	}
}

// snapshot returns a consistent copy of all cached fields under a read lock.
func (c *InstanceCache) snapshot() (
	name string,
	iconURL *string,
	regMode string,
	guildDiscovery string,
	voiceKeyRotationHours int,
	transparencyURL *string,
	logPublicKey *string,
	serverCreationPolicy string,
	screenShareResolutionCap string,
	maxAttachmentBytes int64,
) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var ico *string
	if c.iconURL != nil {
		v := *c.iconURL
		ico = &v
	}
	vkrh := c.voiceKeyRotationHours
	if vkrh == 0 {
		vkrh = voiceKeyRotationHoursDefault
	}
	gd := c.guildDiscovery
	if gd == "" {
		gd = "allowed"
	}
	var tURL *string
	if c.transparencyURL != nil {
		v := *c.transparencyURL
		tURL = &v
	}
	var lPub *string
	if c.logPublicKey != nil {
		v := *c.logPublicKey
		lPub = &v
	}
	scp := c.serverCreationPolicy
	if scp == "" {
		scp = "open"
	}
	ssrc := c.screenShareResolutionCap
	if ssrc == "" {
		ssrc = "1080p"
	}
	mab := c.maxAttachmentBytes
	if mab <= 0 {
		mab = MaxAttachmentBytes
	}
	return c.name, ico, c.registrationMode, gd, vkrh, tURL, lPub, scp, ssrc, mab
}

// handshakeResponse is the JSON shape returned by GET /api/handshake.
type handshakeResponse struct {
	ServerVersion          string `json:"server_version"`
	APIVersion             string `json:"api_version"`
	MinClientVersion       string `json:"min_client_version"`
	KeyPackageLowThreshold int    `json:"key_package_low_threshold"`
	GuildDiscovery         string `json:"guild_discovery"`
	// ServerCreationPolicy controls whether authenticated users may create guilds.
	// Values: "open" (default), "paid" (subscription required), "disabled" (no new guilds).
	ServerCreationPolicy     string          `json:"server_creation_policy"`
	ScreenShareResolutionCap string          `json:"screen_share_resolution_cap"`
	MaxAttachmentBytes       int64           `json:"max_attachment_bytes"`
	Capabilities             map[string]bool `json:"capabilities"`
	// CurrentMLSCiphersuite is the OpenMLS ciphersuite identifier (IANA codepoint)
	// the server currently accepts for new KeyPackages, GroupInfo, Commits, and
	// Welcomes. Clients MUST refuse to upload MLS state created under a different
	// suite. Today the value is MLS_256_XWING_CHACHA20POLY1305_SHA256_Ed25519 (77).
	CurrentMLSCiphersuite int `json:"current_mls_ciphersuite"`
	Name                     string          `json:"name"`
	IconURL                  *string         `json:"iconUrl,omitempty"`
	RegistrationMode         string          `json:"registrationMode"`
	VoiceKeyRotationHours    int             `json:"voice_key_rotation_hours"`
	// TransparencyURL is the base URL of the instance's transparency log API.
	// Omitted when transparency logging is not configured for this instance.
	TransparencyURL *string `json:"transparency_url,omitempty"`
	// LogPublicKey is the hex-encoded Ed25519 public key of the log signer.
	// Clients use this key to verify log countersignatures.
	// Omitted when transparency logging is not configured.
	LogPublicKey *string `json:"log_public_key,omitempty"`
}

// HandshakeHandler returns an http.HandlerFunc that serves GET /api/handshake.
// The endpoint is public (no authentication required) and stateless - it reads
// only from the in-memory cache and version constants, never from the database.
func HandshakeHandler(cache *InstanceCache, voiceEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, iconURL, regMode, guildDiscovery, voiceKeyRotationHours, transparencyURL, logPublicKey, serverCreationPolicy, screenShareResolutionCap, maxAttachmentBytes := cache.snapshot()

		resp := handshakeResponse{
			ServerVersion:            version.ServerVersion,
			APIVersion:               version.APIVersion,
			MinClientVersion:         version.MinClientVersion,
			KeyPackageLowThreshold:   version.KeyPackageLowThreshold,
			GuildDiscovery:           guildDiscovery,
			ServerCreationPolicy:     serverCreationPolicy,
			ScreenShareResolutionCap: screenShareResolutionCap,
			MaxAttachmentBytes:       maxAttachmentBytes,
			Capabilities: map[string]bool{
				"e2ee.chat":      true,
				"e2ee.media":     true,
				"voice.channels": voiceEnabled,
			},
			Name:                  name,
			IconURL:               iconURL,
			RegistrationMode:      regMode,
			VoiceKeyRotationHours: voiceKeyRotationHours,
			TransparencyURL:       transparencyURL,
			LogPublicKey:          logPublicKey,
			CurrentMLSCiphersuite: version.CurrentMLSCiphersuite,
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
