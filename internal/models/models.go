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

// InstanceAdmin is a local instance-operator account for the admin dashboard.
// It is separate from Hush users and cannot be used for end-user authentication.
type InstanceAdmin struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Email        *string    `json:"email,omitempty"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	IsActive     bool       `json:"isActive"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// InstanceAdminSession is a persisted dashboard session for an instance admin.
// Only the opaque cookie token hash is stored server-side.
type InstanceAdminSession struct {
	ID         string     `json:"id"`
	AdminID    string     `json:"adminId"`
	TokenHash  string     `json:"-"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
	CreatedIP  *string    `json:"createdIp,omitempty"`
	UserAgent  *string    `json:"userAgent,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// InstanceServiceIdentity stores the instance-scoped technical Hush identity.
// It is a non-human account and must never be used for dashboard login.
type InstanceServiceIdentity struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	PublicKey          []byte    `json:"-"`
	WrappedPrivateKey  []byte    `json:"-"`
	WrappingKeyVersion string    `json:"wrappingKeyVersion"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// RegisterRequest is the body for POST /api/auth/register.
// PublicKey is the base64-encoded Ed25519 root public key derived from the
// user's BIP39 mnemonic. No password field - identity is cryptographic.
type RegisterRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	PublicKey   string `json:"publicKey"`  // base64-encoded Ed25519 public key
	DeviceID    string `json:"deviceId"`   // optional; generated server-side if empty
	Label       string `json:"label"`      // optional human-readable device name
	InviteCode  string `json:"inviteCode"` // required when registrationMode == "invite_only"
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
	DeviceID  string `json:"deviceId"`  // stable per-device identifier (UUID)
}

// FederatedVerifyRequest is the body for POST /api/auth/federated-verify.
// Used when a user from a foreign instance authenticates on this instance.
type FederatedVerifyRequest struct {
	PublicKey    string `json:"publicKey"`    // base64-encoded Ed25519 public key
	Nonce        string `json:"nonce"`        // hex nonce from /challenge response
	Signature    string `json:"signature"`    // base64-encoded Ed25519 signature over nonce bytes
	HomeInstance string `json:"homeInstance"` // URL of the user's home instance
	Username     string `json:"username"`     // username on the home instance
	DisplayName  string `json:"displayName"`  // display name on the home instance
}

// AuthNonce is a stored challenge nonce pending signature verification.
type AuthNonce struct {
	Nonce         string    `json:"-"`
	UserPublicKey []byte    `json:"-"`
	ExpiresAt     time.Time `json:"-"`
}

// FederatedIdentity represents a user from a foreign Hush instance,
// identified by their Ed25519 public key and home instance URL.
type FederatedIdentity struct {
	ID           string    `json:"id"`
	PublicKey    []byte    `json:"-"`
	HomeInstance string    `json:"homeInstance"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"displayName"`
	CachedAt     time.Time `json:"cachedAt"`
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
	Label           *string    `json:"label,omitempty"`
}

// AuthResponse is returned by register and login (token + user).
type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// GuestAuthResponse is returned by POST /api/auth/guest.
// The guest session is ephemeral - no user record is persisted. The client
// uses ExpiresAt to schedule the 5-minute warning and expiry redirect.
type GuestAuthResponse struct {
	Token     string    `json:"token"`
	GuestID   string    `json:"guestId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// OneTimePreKeyRow is one entry in a batch of one-time pre-keys for upload.
type OneTimePreKeyRow struct {
	KeyID     int    `json:"keyId"`
	PublicKey []byte `json:"publicKey"`
}

// PreKeyUploadRequest is the body for POST /api/keys/upload.
type PreKeyUploadRequest struct {
	DeviceID              string             `json:"deviceId"`
	IdentityKey           []byte             `json:"identityKey"`
	SignedPreKey          []byte             `json:"signedPreKey"`
	SignedPreKeySignature []byte             `json:"signedPreKeySignature"`
	RegistrationID        int                `json:"registrationId"`
	OneTimePreKeys        []OneTimePreKeyRow `json:"oneTimePreKeys"`
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
// Exactly one of SenderID or FederatedSenderID must be non-nil.
type Message struct {
	ID                string    `json:"id"`
	ChannelID         string    `json:"channelId"`
	SenderID          *string   `json:"senderId,omitempty"`
	FederatedSenderID *string   `json:"federatedSenderId,omitempty"`
	RecipientID       *string   `json:"recipientId,omitempty"`
	Ciphertext        []byte    `json:"ciphertext"` // base64-encoded in JSON
	Timestamp         time.Time `json:"timestamp"`
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
// All plaintext name/icon/owner fields are removed - the backend is a blind relay.
// Exception: PublicName and PublicDescription are plaintext when Discoverable=true -
// guild admins explicitly opt in to this exposure by enabling discoverability.
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
	// DM and discovery fields (migration 000021).
	IsDm              bool    `json:"isDm"`
	Category          *string `json:"category,omitempty"`
	PublicName        *string `json:"publicName,omitempty"`
	PublicDescription *string `json:"publicDescription,omitempty"`
}

// ServerMember records a user's (or federated identity's) membership and integer
// permission level within a guild. Exactly one of UserID or FederatedIdentityID must
// be non-nil.
type ServerMember struct {
	ID                  string    `json:"id"`
	ServerID            string    `json:"serverId"`
	UserID              *string   `json:"userId,omitempty"`
	FederatedIdentityID *string   `json:"federatedIdentityId,omitempty"`
	PermissionLevel     int       `json:"permissionLevel"`
	JoinedAt            time.Time `json:"joinedAt"`
}

// ServerMemberWithUser combines user fields with guild membership info for member-list responses.
// HomeInstance is nil for local users and set to the home instance URL for federated users.
type ServerMemberWithUser struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"displayName"`
	CreatedAt       time.Time `json:"createdAt"`
	PermissionLevel int       `json:"permissionLevel"`
	JoinedAt        time.Time `json:"joinedAt"`
	HomeInstance    *string   `json:"homeInstance,omitempty"` // nil = local, set = federated
}

// GuildBillingStats exposes guild infrastructure metrics to the instance operator.
// No guild name, channel list, or member details - privacy boundary is preserved.
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
// OwnerID is removed: instance ownership is API key auth.
// ServerCreationPolicy controls whether authenticated users may create guilds.
type InstanceConfig struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	IconURL              *string   `json:"iconUrl"`
	RegistrationMode     string    `json:"registrationMode"`
	GuildDiscovery       string    `json:"guildDiscovery"`
	ServerCreationPolicy string    `json:"serverCreationPolicy"`
	CreatedAt            time.Time `json:"createdAt"`
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
// Name is removed - it lives in EncryptedMetadata. Type stays plaintext for routing.
type Channel struct {
	ID                string  `json:"id"`
	ServerID          *string `json:"serverId,omitempty"`
	EncryptedMetadata []byte  `json:"encryptedMetadata,omitempty"`
	Type              string  `json:"type"`
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
// Replaces the old ChangeRoleRequest - role string is now an opaque integer.
type ChangePermissionLevelRequest struct {
	UserID          string `json:"userId"`
	PermissionLevel int    `json:"permissionLevel"`
	Reason          string `json:"reason"`
}

// CreateChannelRequest is the body for POST /api/channels.
// Clients should send EncryptedMetadata (MLS-encrypted blob). When MLS is not
// yet bootstrapped, the plaintext Name fallback is accepted and stored as a
// JSON metadata blob so the channel is still usable.
type CreateChannelRequest struct {
	EncryptedMetadata []byte `json:"encryptedMetadata,omitempty"`
	Name              string `json:"name,omitempty"`
	Type              string `json:"type"`
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

// TransparencyLogEntry mirrors one row from the transparency_log_entries table.
// It is returned by GetTransparencyLogEntriesByPubKey for proof generation.
//
// Binary fields (EntryCBOR, LeafHash, LogSig) are excluded from JSON via json:"-".
// Their wire representations are the base64-encoded string fields (EntryCBORB64,
// LeafHashHex, LogSigB64) which are populated by the API handler before writing
// the response. This makes the encoding contract explicit and avoids surprises from
// Go's implicit base64 marshaling of []byte.
type TransparencyLogEntry struct {
	ID         int64     `json:"id"`
	LeafIndex  uint64    `json:"leafIndex"`
	Operation  string    `json:"operation"`
	UserPubKey []byte    `json:"-"`
	SubjectKey []byte    `json:"-"`
	EntryCBOR  []byte    `json:"-"`
	LeafHash   []byte    `json:"-"`
	UserSig    []byte    `json:"-"`
	LogSig     []byte    `json:"-"`
	LoggedAt   time.Time `json:"loggedAt"`

	// Wire-format fields populated by the API handler (never stored).
	// EntryCBORB64 is standard base64 (RFC 4648) for client WebCrypto use.
	// LeafHashHex and LogSigB64 are hex and base64 respectively for display/verify.
	EntryCBORB64  string `json:"entryCbor,omitempty"`
	LeafHashHex   string `json:"leafHash,omitempty"`
	LogSigB64     string `json:"logSig,omitempty"`
	UserPubKeyHex string `json:"userPubKey,omitempty"`
}

// TransparencyTreeHead mirrors one row from the transparency_tree_heads table.
// The most recent row is loaded on startup to reconstruct the Merkle tree fringe.
type TransparencyTreeHead struct {
	TreeSize  uint64    `json:"treeSize"`
	RootHash  []byte    `json:"rootHash"`
	Fringe    []byte    `json:"-"` // serialized [][32]byte - not sent to clients
	HeadSig   []byte    `json:"headSig"`
	CreatedAt time.Time `json:"createdAt"`
}

// MerkleInclusionProof is returned by the transparency verify API endpoint.
// Clients use WebCrypto SHA-256 to recompute the root from the audit path.
//
// Binary fields are excluded from JSON (json:"-"); their wire representations
// use explicit encoding:
//   - AuditPathHex: each 32-byte sibling hash as a lowercase hex string.
//   - RootHashHex:  32-byte root as a lowercase hex string.
//   - LogSignatureB64: 64-byte Ed25519 signature as standard base64.
//
// Hex is used for AuditPath and RootHash because the client's verifyInclusion()
// function passes sibling hashes to hexToBytes() and compares the recomputed root
// with bytesToHex(). Base64 is used for LogSignature because base64ToBytes() is
// called on it. These encodings must match the client's transparencyVerifier.js.
type MerkleInclusionProof struct {
	LeafIndex uint64   `json:"leafIndex"`
	TreeSize  uint64   `json:"treeSize"`
	AuditPath [][]byte `json:"-"` // raw binary; use AuditPathHex for JSON
	RootHash  []byte   `json:"-"` // raw binary; use RootHashHex for JSON
	LogSig    []byte   `json:"-"` // raw binary; use LogSignatureB64 for JSON

	// Wire-format fields populated by the API handler (never stored).
	AuditPathHex    []string `json:"auditPath,omitempty"`    // hex-encoded sibling hashes
	RootHashHex     string   `json:"rootHash,omitempty"`     // hex-encoded root
	LogSignatureB64 string   `json:"logSignature,omitempty"` // base64-encoded log signature
}

// CreateDMRequest is the body for POST /api/guilds/dm.
type CreateDMRequest struct {
	OtherUserID string `json:"otherUserId"`
}

// DiscoverGuild is one card in the GET /api/guilds/discover response.
// Only fields that discoverable guild admins have opted to expose are included.
type DiscoverGuild struct {
	ID                string    `json:"id"`
	PublicName        string    `json:"publicName"`
	PublicDescription string    `json:"publicDescription"`
	Category          string    `json:"category"`
	AccessPolicy      string    `json:"accessPolicy"`
	MemberCount       int       `json:"memberCount"`
	CreatedAt         time.Time `json:"createdAt"`
}

// UserSearchPublicResult is a user record safe for public search endpoints.
// Contains only identity fields - no ban status, roles, or credentials.
type UserSearchPublicResult struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

// DMResponse is returned by POST /api/guilds/dm.
// Wraps the DM guild server with the other participant's identity.
type DMResponse struct {
	Server    Server                 `json:"server"`
	OtherUser UserSearchPublicResult `json:"otherUser"`
	ChannelID string                 `json:"channelId"`
}
