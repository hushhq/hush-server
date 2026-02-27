package models

import "time"

// User is the domain user. PasswordHash is nil for OAuth/guest users.
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash *string    `json:"-"`
	DisplayName  string     `json:"displayName"`
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
	ID         string    `json:"id"`
	ChannelID  string    `json:"channelId"`
	SenderID   string    `json:"senderId"`
	RecipientID *string  `json:"recipientId,omitempty"`
	Ciphertext []byte    `json:"ciphertext"` // base64-encoded in JSON
	Timestamp  time.Time `json:"timestamp"`
}

// Server is a Discord-like server.
type Server struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IconURL   *string   `json:"iconUrl,omitempty"`
	OwnerID   string    `json:"ownerId"`
	CreatedAt time.Time `json:"createdAt"`
}

// ServerMember is a user's membership in a server.
type ServerMember struct {
	ServerID string    `json:"serverId"`
	UserID   string    `json:"userId"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
}

// ServerMemberWithUser is a server member with display name for list responses.
type ServerMemberWithUser struct {
	UserID      string    `json:"userId"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joinedAt"`
}

// ServerWithRole embeds Server and adds the current user's role.
type ServerWithRole struct {
	Server
	Role string `json:"role"`
}

// Channel is a text or voice channel within a server.
type Channel struct {
	ID        string  `json:"id"`
	ServerID  string  `json:"serverId"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	VoiceMode *string `json:"voiceMode,omitempty"`
	ParentID  *string `json:"parentId,omitempty"`
	Position  int     `json:"position"`
}

// InviteCode is an invite link token for a server.
type InviteCode struct {
	Code      string    `json:"code"`
	ServerID  string    `json:"serverId"`
	CreatedBy string    `json:"createdBy"`
	ExpiresAt time.Time `json:"expiresAt"`
	MaxUses   int       `json:"maxUses"`
	Uses      int       `json:"uses"`
}

// CreateServerRequest is the body for POST /api/servers.
type CreateServerRequest struct {
	Name    string  `json:"name"`
	IconURL *string `json:"iconUrl,omitempty"`
}

// UpdateServerRequest is the body for PUT /api/servers/:id.
type UpdateServerRequest struct {
	Name    *string `json:"name,omitempty"`
	IconURL *string `json:"iconUrl,omitempty"`
}

// CreateChannelRequest is the body for POST /api/servers/:id/channels.
type CreateChannelRequest struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	VoiceMode *string `json:"voiceMode,omitempty"`
	ParentID  *string `json:"parentId,omitempty"`
	Position  *int    `json:"position,omitempty"`
}

// JoinServerRequest is the body for POST /api/servers/:id/join.
type JoinServerRequest struct {
	InviteCode string `json:"inviteCode"`
}

// CreateInviteRequest is the body for POST /api/servers/:id/invites.
type CreateInviteRequest struct {
	MaxUses   *int `json:"maxUses,omitempty"`
	ExpiresIn *int `json:"expiresIn,omitempty"` // seconds
}
