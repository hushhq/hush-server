package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const serviceIdentityWrappingKeyVersion = "aesgcm-v1"

// WrapServiceIdentityPrivateKey encrypts the private key for at-rest storage.
func WrapServiceIdentityPrivateKey(privateKey []byte, masterKey string) ([]byte, string, error) {
	keyBytes, err := decodeSymmetricKey(masterKey)
	if err != nil {
		return nil, "", err
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, "", fmt.Errorf("create service identity cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", fmt.Errorf("create service identity AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("generate service identity nonce: %w", err)
	}
	wrapped := aead.Seal(nonce, nonce, privateKey, nil)
	return wrapped, serviceIdentityWrappingKeyVersion, nil
}

// GenerateServiceIdentity creates a new Ed25519 keypair for technical service use.
func GenerateServiceIdentity() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate service identity: %w", err)
	}
	return publicKey, privateKey, nil
}

func decodeSymmetricKey(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("service identity master key is required")
	}
	if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("service identity master key must decode to 32 bytes")
}
