package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims holds JWT claims for Hush auth.
type Claims struct {
	jwt.RegisteredClaims
	// SessionID is the session UUID used to look up the session record in the DB.
	// Guest sessions have no DB record; for guests, SessionID holds the guest UUID.
	SessionID string `json:"sid"`
	// IsGuest marks a short-lived ephemeral guest session. Guest sessions are
	// validated by JWT signature only — no DB session record is required.
	IsGuest bool `json:"is_guest,omitempty"`
}

// SignJWT builds and signs a JWT for the user/session. Expires at expiresAt.
func SignJWT(userID, sessionID, secret string, expiresAt time.Time) (string, error) {
	return signJWT(userID, sessionID, secret, expiresAt, false)
}

// SignGuestJWT builds and signs a short-lived guest JWT. The token is
// validated by signature only — no DB session record is stored.
func SignGuestJWT(guestID, sessionID, secret string, expiresAt time.Time) (string, error) {
	return signJWT(guestID, sessionID, secret, expiresAt, true)
}

func signJWT(userID, sessionID, secret string, expiresAt time.Time, isGuest bool) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        sessionID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		SessionID: sessionID,
		IsGuest:   isGuest,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(secret))
}

// ValidateJWT parses and validates the token, returns userID, sessionID, and
// whether the token is a guest token.
func ValidateJWT(tokenString, secret string) (userID, sessionID string, isGuest bool, err error) {
	tok, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", "", false, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return "", "", false, fmt.Errorf("invalid token")
	}
	return claims.Subject, claims.SessionID, claims.IsGuest, nil
}

// TokenHash returns a deterministic hash of the token for storage/lookup.
func TokenHash(tokenString string) string {
	h := sha256.Sum256([]byte(tokenString))
	return hex.EncodeToString(h[:])
}
