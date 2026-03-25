package models

import "time"

// User is the domain user. RootPublicKey is the Ed25519 root public key
// derived from the user's BIP39 mnemonic; never included in JSON responses.
type User struct {
	ID            string    `json:"id"`
	Username      string    `json:"username"`
	RootPublicKey []byte    `json:"-"`
	DisplayName   string    `json:"displayName"`
	Role          string    `json:"role"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Session is a stored session (token_hash, expires_at). ID is the session UUID.
type Session struct {
	ID        string    `json:"-"`
	UserID    string    `json:"-"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"-"`
}

// RegisterRequest is the body for POST /api/auth/register.
// PublicKey is the base64-encoded Ed25519 root public key derived from the
// user's BIP39 mnemonic. No password field — identity is cryptographic.
type RegisterRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	PublicKey   string `json:"publicKey"`   // base64-encoded Ed25519 public key
	DeviceID    string `json:"deviceId"`    // optional; generated server-side if empty
	InviteCode  string `json:"inviteCode"`  // required when registrationMode == "invite_only"
}

// ChallengeRequest is the body for POST /api/auth/challenge.
// The server returns a short-lived nonce that the client must sign.
type ChallengeRequest struct {
	PublicKey string `json:"publicKey"` // base64-encoded Ed25519 public key
}

// VerifyRequest is the body for POST /api/auth/verify.
// The client signs the nonce received from /challenge and submits it here.
type VerifyRequest struct {
	PublicKey string `json:"publicKey"` // base64-encoded Ed25519 public key
	Nonce     string `json:"nonce"`     // hex nonce from /challenge response
	Signature string `json:"signature"` // base64-encoded Ed25519 signature over nonce bytes
}

// AuthNonce is a stored challenge nonce pending signature verification.
type AuthNonce struct {
	Nonce         string    `json:"-"`
	UserPublicKey []byte    `json:"-"`
	ExpiresAt     time.Time `json:"-"`
}

// DeviceKey records a certified public key for a user's device.
// The first device (registered with root key) has a nil Certificate.
// Subsequent devices must carry a Certificate = Sign(root_priv, device_pub).
type DeviceKey struct {
	ID              string     `json:"id"`
	UserID          string     `json:"userId"`
	DeviceID        string     `json:"deviceId"`
	DevicePublicKey []byte     `json:"-"` // never sent in JSON responses
	Certificate     []byte     `json:"-"` // root-key signature over DevicePublicKey; nil for first device
	CertifiedAt     time.Time  `json:"certifiedAt"`
	LastSeen        *time.Time `json:"lastSeen,omitempty"`
	Label           string     `json:"label,omitempty"`
}

// AuthResponse is returned by register and login (token + user).
type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// OneTimePreKeyRow is one entry in a batch of one-time pre-keys for upload.
type OneTimePreKeyRow struct {
	KeyID     int    `json:"keyId"`
	PublicKey []byte `json:"publicKey"`
}

// PreKeyUploadRequest is the body for POST /api/keys/upload.
type PreKeyUploadRequest struct {
	DeviceID               string             `json:"deviceId"`
	IdentityKey             []byte             `json:"identityKey"`
	SignedPreKey            []byte             `json:"signedPreKey"`
	SignedPreKeySignature   []byte             `json:"signedPreKeySignature"`
	RegistrationID          int                `json:"registrationId"`
	OneTimePreKeys          []OneTimePreKeyRow `json:"oneTimePreKeys"`
}

// PreKeyBundle is returned by GET /api/keys/:userId and GET /api/keys/:userId/:deviceId.
type PreKeyBundle struct {
	IdentityKey           []byte `json:"identityKey"`
	SignedPreKey          []byte `json:"signedPreKey"`
	SignedPreKeySignature []byte `json:"signedPreKeySignature"`
	SignedPreKeyID        int    `json:"signedPreKeyId"`
	RegistrationID        int    `json:"registrationId"`
	OneTimePreKeyID       *int   `json:"oneTimePreKeyId,omitempty"`
	OneTimePreKey         []byte `json:"oneTimePreKey,omitempty"`
}

// Message is a stored encrypted message. Ciphertext is opaque to the server.
// RecipientID is nil for broadcast/single-ciphertext; set for fan-out per recipient.
type Message struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channelId"`
	SenderID    string    `json:"senderId"`
	RecipientID *string   `json:"recipientId,omitempty"`
	Ciphertext  []byte    `json:"ciphertext"` // base64-encoded in JSON
	Timestamp   time.Time `json:"timestamp"`
}

// SystemMessage is an event log entry in a guild's #system channel.
type SystemMessage struct {
	ID        string                 `json:"id"`
	ServerID  string                 `json:"serverId"`
	EventType string                 `json:"eventType"`
	ActorID   string                 `json:"actorId"`
	TargetID  *string                `json:"targetId,omitempty"`
	Reason    string                 `json:"reason"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"createdAt"`
}

// Permission level constants for server_members.permission_level.
// Human-readable role labels are stored encrypted in MLS group state.
const (
	PermissionLevelMember = 0
	PermissionLevelMod    = 1
	PermissionLevelAdmin  = 2
	PermissionLevelOwner  = 3
)

// Server is a guild within this Hush instance.
// All plaintext name/icon/owner fields are removed — the backend is a blind relay.
type Server struct {
	ID                  string     `json:"id"`
	EncryptedMetadata   []byte     `json:"encryptedMetadata,omitempty"`
	MemberCount         int        `json:"memberCount"`
	TextChannelCount    int        `json:"textChannelCount"`
	VoiceChannelCount   int        `json:"voiceChannelCount"`
	StorageBytes        int64      `json:"storageBytes"`
	MessageCount        int64      `json:"messageCount"`
	ActiveMembers30d    int        `json:"activeMembers30d"`
	LastActiveAt        *time.Time `json:"lastActiveAt,omitempty"`
	AccessPolicy        string     `json:"accessPolicy"`
	Discoverable        bool       `json:"discoverable"`
	AdminLabelEncrypted []byte     `json:"adminLabelEncrypted,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// ServerMember records a user's membership and integer permission level within a guild.
type ServerMember struct {
	ServerID        string    `json:"serverId"`
	UserID          string    `json:"userId"`
	PermissionLevel int       `json:"permissionLevel"`
	JoinedAt        time.Time `json:"joinedAt"`
}

// ServerMemberWithUser combines user fields with guild membership info for member-list responses.
type ServerMemberWithUser struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"displayName"`
	CreatedAt       time.Time `json:"createdAt"`
	PermissionLevel int       `json:"permissionLevel"`
	JoinedAt        time.Time `json:"joinedAt"`
}

// GuildBillingStats exposes guild infrastructure metrics to the instance operator.
// No guild name, channel list, or member details — privacy boundary is preserved.
type GuildBillingStats struct {
	ID               string     `json:"id"`
	MemberCount      int        `json:"memberCount"`
	StorageBytes     int64      `json:"storageBytes"`
	MessageCount     int64      `json:"messageCount"`
	ActiveMembers30d int        `json:"activeMembers30d"`
	LastActiveAt     *time.Time `json:"lastActiveAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

// CreateServerRequest is the body for POST /api/servers.
// EncryptedMetadata may be nil on creation if the client has not yet set up the
// guild metadata MLS group (two-step creation flow).
type CreateServerRequest struct {
	EncryptedMetadata []byte  `json:"encryptedMetadata,omitempty"`
	Name              string  `json:"name,omitempty"`
	TemplateID        *string `json:"templateId,omitempty"`
}

// TemplateChannel describes a single channel in a server creation template.
type TemplateChannel struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	VoiceMode *string `json:"voiceMode,omitempty"`
	ParentRef *string `json:"parentRef,omitempty"`
	Position  int     `json:"position"`
}

// ServerTemplate is a named, reusable channel template for guild creation.
type ServerTemplate struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Channels  []TemplateChannel `json:"channels"`
	IsDefault bool              `json:"isDefault"`
	Position  int               `json:"position"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// InstanceConfig is the single-row table that describes this Hush instance.
// OwnerID and ServerCreationPolicy are removed: instance ownership is API key auth;
// creation policy is no longer an instance-level concern.
type InstanceConfig struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	IconURL          *string   `json:"iconUrl"`
	RegistrationMode string    `json:"registrationMode"`
	GuildDiscovery   string    `json:"guildDiscovery"`
	CreatedAt        time.Time `json:"createdAt"`
}

// Member is a user with their instance role, used for member list responses.
type Member struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Channel is a text or voice channel within a guild.
// Name is removed — it lives in EncryptedMetadata. Type stays plaintext for routing.
type Channel struct {
	ID                string  `json:"id"`
	ServerID          *string `json:"serverId,omitempty"`
	EncryptedMetadata []byte  `json:"encryptedMetadata,omitempty"`
	Type              string  `json:"type"`
	VoiceMode         *string `json:"voiceMode,omitempty"`
	ParentID          *string `json:"parentId,omitempty"`
	Position          int     `json:"position"`
}

// InviteCode is an invite link token for the instance or a specific guild.
type InviteCode struct {
	Code      string    `json:"code"`
	ServerID  *string   `json:"serverId,omitempty"`
	CreatedBy string    `json:"createdBy"`
	ExpiresAt time.Time `json:"expiresAt"`
	MaxUses   int       `json:"maxUses"`
	Uses      int       `json:"uses"`
}

// Ban represents an active or historical ban record.
type Ban struct {
	ID        string     `json:"id"`
	ServerID  *string    `json:"serverId,omitempty"`
	UserID    string     `json:"userId"`
	ActorID   string     `json:"actorId"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	LiftedAt  *time.Time `json:"liftedAt,omitempty"`
	LiftedBy  *string    `json:"liftedBy,omitempty"`
}

// Mute represents an active or historical mute record (text AND voice).
type Mute struct {
	ID        string     `json:"id"`
	ServerID  *string    `json:"serverId,omitempty"`
	UserID    string     `json:"userId"`
	ActorID   string     `json:"actorId"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	LiftedAt  *time.Time `json:"liftedAt,omitempty"`
	LiftedBy  *string    `json:"liftedBy,omitempty"`
}

// AuditLogEntry records a single moderation action.
type AuditLogEntry struct {
	ID        string                 `json:"id"`
	ServerID  *string                `json:"serverId,omitempty"`
	ActorID   string                 `json:"actorId"`
	TargetID  *string                `json:"targetId,omitempty"`
	Action    string                 `json:"action"`
	Reason    string                 `json:"reason"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"createdAt"`
}

// KickRequest is the body for POST /api/moderation/kick.
type KickRequest struct {
	UserID string `json:"userId"`
	Reason string `json:"reason"`
}

// BanRequest is the body for POST /api/moderation/ban.
type BanRequest struct {
	UserID    string `json:"userId"`
	Reason    string `json:"reason"`
	ExpiresIn *int   `json:"expiresIn,omitempty"` // seconds; nil = permanent
}

// MuteRequest is the body for POST /api/moderation/mute.
type MuteRequest struct {
	UserID    string `json:"userId"`
	Reason    string `json:"reason"`
	ExpiresIn *int   `json:"expiresIn,omitempty"` // seconds; nil = permanent
}

// UnbanRequest is the body for POST /api/moderation/unban.
type UnbanRequest struct {
	UserID string `json:"userId"`
	Reason string `json:"reason"`
}

// UnmuteRequest is the body for POST /api/moderation/unmute.
type UnmuteRequest struct {
	UserID string `json:"userId"`
	Reason string `json:"reason"`
}

// ChangePermissionLevelRequest is the body for PUT /api/servers/:id/members/:userId/level.
// Replaces the old ChangeRoleRequest — role string is now an opaque integer.
type ChangePermissionLevelRequest struct {
	UserID           string `json:"userId"`
	PermissionLevel  int    `json:"permissionLevel"`
	Reason           string `json:"reason"`
}

// CreateChannelRequest is the body for POST /api/channels.
// Clients should send EncryptedMetadata (MLS-encrypted blob). When MLS is not
// yet bootstrapped, the plaintext Name fallback is accepted and stored as a
// JSON metadata blob so the channel is still usable.
type CreateChannelRequest struct {
	EncryptedMetadata []byte  `json:"encryptedMetadata,omitempty"`
	Name              string  `json:"name,omitempty"`
	Type              string  `json:"type"`
	VoiceMode         *string `json:"voiceMode,omitempty"`
	ParentID          *string `json:"parentId,omitempty"`
	Position          *int    `json:"position,omitempty"`
}

// MoveChannelRequest is the body for PUT /api/channels/:id/move.
type MoveChannelRequest struct {
	ParentID *string `json:"parentId"`
	Position int     `json:"position"`
}

// CreateInviteRequest is the body for POST /api/invites.
type CreateInviteRequest struct {
	MaxUses   *int `json:"maxUses,omitempty"`
	ExpiresIn *int `json:"expiresIn,omitempty"` // seconds
}

// UpdateInstanceRequest is the body for PATCH /api/instance.
// ServerCreationPolicy removed; GuildDiscovery added.
type UpdateInstanceRequest struct {
	Name             *string `json:"name,omitempty"`
	IconURL          *string `json:"iconUrl,omitempty"`
	RegistrationMode *string `json:"registrationMode,omitempty"`
	GuildDiscovery   *string `json:"guildDiscovery,omitempty"`
}

// InstanceBan is an instance-level ban record (separate from guild bans).
type InstanceBan struct {
	ID        string     `json:"id"`
	UserID    string     `json:"userId"`
	ActorID   string     `json:"actorId"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	LiftedAt  *time.Time `json:"liftedAt,omitempty"`
	LiftedBy  *string    `json:"liftedBy,omitempty"`
}

// InstanceAuditLogEntry records an instance-level admin action.
type InstanceAuditLogEntry struct {
	ID        string                 `json:"id"`
	ActorID   string                 `json:"actorId"`
	TargetID  *string                `json:"targetId,omitempty"`
	Action    string                 `json:"action"`
	Reason    string                 `json:"reason"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"createdAt"`
}

// UserSearchResult is returned by the admin user search endpoint.
type UserSearchResult struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	DisplayName  string     `json:"displayName"`
	Role         string     `json:"role"`
	CreatedAt    time.Time  `json:"createdAt"`
	IsBanned     bool       `json:"isBanned"`
	BanReason    *string    `json:"banReason,omitempty"`
	BanExpiresAt *time.Time `json:"banExpiresAt,omitempty"`
}

// InstanceBanRequest is the body for POST /api/instance/bans.
type InstanceBanRequest struct {
	UserID    string `json:"userId"`
	Reason    string `json:"reason"`
	ExpiresIn *int   `json:"expiresIn,omitempty"` // seconds; nil = permanent
}

// InstanceUnbanRequest is the body for POST /api/instance/unban.
type InstanceUnbanRequest struct {
	UserID string `json:"userId"`
	Reason string `json:"reason"`
}
