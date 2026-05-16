package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// NonceLen is the number of random bytes in each challenge nonce.
const NonceLen = 32

// ChallengeV2Header is the domain-separation tag for the v2 auth challenge
// signature payload. It binds the signature to the auth challenge purpose
// and prevents a v2 signature from being mistaken for any other Ed25519
// payload signed by the same identity key.
const ChallengeV2Header = "HUSH-AUTH-CHALLENGE-V2"

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
// nonce (expressed as a hex string) under publicKey. This is the legacy v1
// challenge protocol: the signed message is the raw nonce bytes with no
// audience binding. It is retained ONLY for backward compatibility with
// pre-v2 clients and must never be invoked when the request carries any v2
// fields (challengeVersion == 2 or audience present).
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

// BuildChallengeV2Payload returns the canonical UTF-8 byte encoding of the
// v2 auth-challenge signature payload:
//
//	HUSH-AUTH-CHALLENGE-V2\naudience=<origin>\nnonce=<hex>
//
// Both client and server MUST produce the same bytes from the same
// (nonce, audience) pair. The audience is expected to already be in
// normalized form (see NormalizeAudience); this function does not
// re-normalize so callers can pin the exact bytes that get signed.
func BuildChallengeV2Payload(nonceHex, audience string) []byte {
	return []byte(ChallengeV2Header + "\naudience=" + audience + "\nnonce=" + nonceHex)
}

// VerifyChallengeV2Signature verifies an Ed25519 signature over the v2
// challenge payload produced by BuildChallengeV2Payload(nonceHex, audience).
//
// audience MUST be the caller-validated, normalized expected audience for
// this request. The caller is responsible for rejecting requests whose
// declared audience does not match the server's canonical public API
// origin BEFORE calling this function; the verifier itself only attests
// that the signature is a valid Ed25519 signature over the v2 payload for
// the given (nonceHex, audience).
//
// Returns nil on success.
func VerifyChallengeV2Signature(publicKey []byte, nonceHex, audience string, signature []byte) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key length: got %d, want %d", len(publicKey), ed25519.PublicKeySize)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature length: got %d, want %d", len(signature), ed25519.SignatureSize)
	}
	if strings.TrimSpace(audience) == "" {
		return errors.New("v2 audience required")
	}
	if _, err := hex.DecodeString(nonceHex); err != nil {
		return fmt.Errorf("invalid nonce hex: %w", err)
	}
	payload := BuildChallengeV2Payload(nonceHex, audience)
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("v2 signature verification failed")
	}
	return nil
}

// NormalizeAudience reduces an audience string to scheme://host[:port]
// form with the scheme and host lowercased, the default port for the
// scheme stripped, and any path / query / fragment removed.
//
// Returns the normalized form, or "" when the input is empty, missing
// a scheme/host, uses a scheme other than http/https, or otherwise
// fails to parse. The empty-string return is the signal that callers
// should treat as "unusable" — never compare a normalized audience
// against "" as if it were a valid origin.
func NormalizeAudience(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + host + ":" + port
	}
	return scheme + "://" + host
}
