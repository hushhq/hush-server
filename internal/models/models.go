package models

import "time"

// User is the domain user. PasswordHash is nil for OAuth/guest users.
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash *string    `json:"-"`
	DisplayName  string     `json:"displayName"`
	Role         string     `json:"role"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// Session is a stored session (token_hash, expires_at). ID is the session UUID.
type Session struct {
	ID        string    `json:"-"`
	UserID    string    `json:"-"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"-"`
}

// RegisterRequest is the body for POST /api/auth/register.
type RegisterRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

// LoginRequest is the body for POST /api/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse is returned by register, login, guest (token + user).
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

// InstanceConfig is the single-row table that describes this Hush instance.
type InstanceConfig struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	IconURL          *string   `json:"iconUrl"`
	OwnerID          *string   `json:"ownerId"`
	RegistrationMode string    `json:"registrationMode"`
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

// Channel is a text or voice channel within the instance.
type Channel struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	VoiceMode *string `json:"voiceMode,omitempty"`
	ParentID  *string `json:"parentId,omitempty"`
	Position  int     `json:"position"`
}

// InviteCode is an invite link token for the instance.
type InviteCode struct {
	Code      string    `json:"code"`
	CreatedBy string    `json:"createdBy"`
	ExpiresAt time.Time `json:"expiresAt"`
	MaxUses   int       `json:"maxUses"`
	Uses      int       `json:"uses"`
}

// Ban represents an active or historical ban record.
type Ban struct {
	ID        string     `json:"id"`
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

// ChangeRoleRequest is the body for PUT /api/moderation/role.
type ChangeRoleRequest struct {
	UserID  string `json:"userId"`
	NewRole string `json:"newRole"`
	Reason  string `json:"reason"`
}

// CreateChannelRequest is the body for POST /api/channels.
type CreateChannelRequest struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	VoiceMode *string `json:"voiceMode,omitempty"`
	ParentID  *string `json:"parentId,omitempty"`
	Position  *int    `json:"position,omitempty"`
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
type UpdateInstanceRequest struct {
	Name             *string `json:"name,omitempty"`
	IconURL          *string `json:"iconUrl,omitempty"`
	RegistrationMode *string `json:"registrationMode,omitempty"`
}
