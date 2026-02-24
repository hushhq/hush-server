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
