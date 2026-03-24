package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NonceLen is the number of random bytes in each challenge nonce.
const NonceLen = 32

// GenerateNonce returns a 64-character lowercase hex string backed by 32
// cryptographically random bytes. The nonce is suitable for use in the
// BIP39 challenge-response authentication flow.
func GenerateNonce() (string, error) {
	b := make([]byte, NonceLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// VerifySignature verifies that signature is a valid Ed25519 signature of the
// nonce (expressed as a hex string) under publicKey.
//
// Returns nil on success, non-nil error on any verification failure including:
//   - publicKey is not exactly ed25519.PublicKeySize (32) bytes
//   - signature is not exactly ed25519.SignatureSize (64) bytes
//   - nonceHex is not valid lowercase hex
//   - the signature does not verify
func VerifySignature(publicKey []byte, nonceHex string, signature []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length: got %d, want %d", len(publicKey), ed25519.PublicKeySize)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature length: got %d, want %d", len(signature), ed25519.SignatureSize)
	}
	nonceBytes, err := hex.DecodeString(nonceHex)
	if err != nil {
		return fmt.Errorf("invalid nonce hex: %w", err)
	}
	if !ed25519.Verify(publicKey, nonceBytes, signature) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
