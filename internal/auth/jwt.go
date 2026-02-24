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
	SessionID string `json:"sid"`
}

// SignJWT builds and signs a JWT for the user/session. Expires at expiresAt.
func SignJWT(userID, sessionID, secret string, expiresAt time.Time) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        sessionID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		SessionID: sessionID,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(secret))
}

// ValidateJWT parses and validates the token, returns userID and sessionID.
func ValidateJWT(tokenString, secret string) (userID, sessionID string, err error) {
	tok, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", "", err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return "", "", fmt.Errorf("invalid token")
	}
	return claims.Subject, claims.SessionID, nil
}

// TokenHash returns a deterministic hash of the token for storage/lookup.
func TokenHash(tokenString string) string {
	h := sha256.Sum256([]byte(tokenString))
	return hex.EncodeToString(h[:])
}
