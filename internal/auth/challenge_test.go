package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateNonce_Length(t *testing.T) {
	nonce, err := GenerateNonce()
	require.NoError(t, err)
	require.Len(t, nonce, 64, "nonce must be 64 hex characters (32 bytes)")
}

func TestGenerateNonce_Unique(t *testing.T) {
	a, err := GenerateNonce()
	require.NoError(t, err)
	b, err := GenerateNonce()
	require.NoError(t, err)
	require.NotEqual(t, a, b, "consecutive nonces must not be identical")
}

func TestVerifySignature_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)

	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	sig := ed25519.Sign(priv, nonceBytes)

	err = VerifySignature(pub, nonce, sig)
	require.NoError(t, err)
}

func TestVerifySignature_BadSignature(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)

	// Sign with a different key to produce an invalid signature.
	_, priv2, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	sig := ed25519.Sign(priv2, nonceBytes)

	err = VerifySignature(pub, nonce, sig)
	require.Error(t, err)
}

func TestVerifySignature_BadPublicKeyLength(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)

	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	sig := ed25519.Sign(priv, nonceBytes)

	shortKey := make([]byte, 16) // should be 32 bytes
	err = VerifySignature(shortKey, nonce, sig)
	require.Error(t, err)
}

func TestVerifySignature_BadSignatureLength(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)

	shortSig := make([]byte, 32) // should be 64 bytes
	err = VerifySignature(pub, nonce, shortSig)
	require.Error(t, err)
}

func TestVerifySignature_BadNonceHex(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	sig := make([]byte, ed25519.SignatureSize)
	err = VerifySignature(pub, "not-valid-hex!!", sig)
	require.Error(t, err)
}
