package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

const adminSessionTokenSize = 32

// GenerateSessionToken returns a cryptographically random opaque token for cookie sessions.
func GenerateSessionToken() (string, error) {
	token := make([]byte, adminSessionTokenSize)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}
