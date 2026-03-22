package api

import (
	"net/http"
	"sync"

	"hush.app/server/internal/version"
)

// InstanceCache holds a snapshot of the instance_config row in memory so the
// handshake handler never hits the database. It is populated on server startup
// and refreshed whenever the instance config is updated via PUT /api/instance.
type InstanceCache struct {
	mu                   sync.RWMutex
	name                 string
	iconURL              *string
	registrationMode     string
	serverCreationPolicy string
	bootstrapped         bool
	voiceKeyRotationHours int
}

// voiceKeyRotationHoursDefault is the default voice group key rotation interval.
// Matches the instance_config column default (2 hours).
const voiceKeyRotationHoursDefault = 2

// NewInstanceCache creates an empty cache. Zero values are safe: bootstrapped=false,
// name="" — representing a fresh instance before first-user setup.
// voiceKeyRotationHours defaults to voiceKeyRotationHoursDefault.
func NewInstanceCache() *InstanceCache {
	return &InstanceCache{voiceKeyRotationHours: voiceKeyRotationHoursDefault}
}

// Set updates all cached fields under a write lock. Called on startup (from
// GetInstanceConfig) and after updateConfig writes to the database.
func (c *InstanceCache) Set(name string, iconURL *string, regMode string, serverCreationPolicy string, bootstrapped bool, voiceKeyRotationHours int) {
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
	c.serverCreationPolicy = serverCreationPolicy
	c.bootstrapped = bootstrapped
	if voiceKeyRotationHours > 0 {
		c.voiceKeyRotationHours = voiceKeyRotationHours
	} else {
		c.voiceKeyRotationHours = voiceKeyRotationHoursDefault
	}
}

// snapshot returns a consistent copy of all cached fields under a read lock.
func (c *InstanceCache) snapshot() (name string, iconURL *string, regMode string, serverCreationPolicy string, bootstrapped bool, voiceKeyRotationHours int) {
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
	return c.name, ico, c.registrationMode, c.serverCreationPolicy, c.bootstrapped, vkrh
}

// handshakeResponse is the JSON shape returned by GET /api/handshake.
type handshakeResponse struct {
	ServerVersion         string          `json:"server_version"`
	APIVersion            string          `json:"api_version"`
	MinClientVersion      string          `json:"min_client_version"`
	KeyPackageLowThreshold int            `json:"key_package_low_threshold"`
	ServerCreationPolicy  string          `json:"server_creation_policy"`
	Capabilities          map[string]bool `json:"capabilities"`
	Name                  string          `json:"name"`
	IconURL               *string         `json:"iconUrl,omitempty"`
	RegistrationMode      string          `json:"registrationMode"`
	Bootstrapped          bool            `json:"bootstrapped"`
	VoiceKeyRotationHours int             `json:"voice_key_rotation_hours"`
}

// HandshakeHandler returns an http.HandlerFunc that serves GET /api/handshake.
// The endpoint is public (no authentication required) and stateless — it reads
// only from the in-memory cache and version constants, never from the database.
func HandshakeHandler(cache *InstanceCache, voiceEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, iconURL, regMode, scp, bootstrapped, voiceKeyRotationHours := cache.snapshot()

		resp := handshakeResponse{
			ServerVersion:          version.ServerVersion,
			APIVersion:             version.APIVersion,
			MinClientVersion:       version.MinClientVersion,
			KeyPackageLowThreshold: version.KeyPackageLowThreshold,
			ServerCreationPolicy:   scp,
			Capabilities: map[string]bool{
				"e2ee.chat":      true,
				"e2ee.media":     true,
				"voice.channels": voiceEnabled,
			},
			Name:                  name,
			IconURL:               iconURL,
			RegistrationMode:      regMode,
			Bootstrapped:          bootstrapped,
			VoiceKeyRotationHours: voiceKeyRotationHours,
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
