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

// ----- v2 (audience-bound) signature payload -----

func TestBuildChallengeV2Payload_StableFormat(t *testing.T) {
	got := string(BuildChallengeV2Payload("deadbeef", "https://home.example"))
	want := "HUSH-AUTH-CHALLENGE-V2\naudience=https://home.example\nnonce=deadbeef"
	require.Equal(t, want, got, "v2 payload format must stay byte-identical to its spec")
}

func TestBuildChallengeV2Payload_DifferentAudience_DifferentBytes(t *testing.T) {
	a := BuildChallengeV2Payload("deadbeef", "https://home.example")
	b := BuildChallengeV2Payload("deadbeef", "https://evil.example")
	require.NotEqual(t, a, b)
}

func TestVerifyChallengeV2Signature_Valid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)
	audience := "https://home.example"

	sig := ed25519.Sign(priv, BuildChallengeV2Payload(nonce, audience))

	require.NoError(t, VerifyChallengeV2Signature(pub, nonce, audience, sig))
}

func TestVerifyChallengeV2Signature_WrongAudience_Rejects(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)

	// Signed for the evil instance, but the (legitimate) home instance
	// will try to verify under its own audience. Replay must fail.
	sig := ed25519.Sign(priv, BuildChallengeV2Payload(nonce, "https://evil.example"))
	require.Error(t, VerifyChallengeV2Signature(pub, nonce, "https://home.example", sig))
}

func TestVerifyChallengeV2Signature_EmptyAudience_Rejects(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)

	sig := ed25519.Sign(priv, BuildChallengeV2Payload(nonce, ""))
	require.Error(t, VerifyChallengeV2Signature(pub, nonce, "", sig))
}

func TestVerifyChallengeV2Signature_RawNonceSig_Rejects(t *testing.T) {
	// A v1 (raw-nonce) signature must never satisfy the v2 verifier,
	// even if the attacker also supplies a plausible audience string.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	nonce, err := GenerateNonce()
	require.NoError(t, err)
	nonceBytes, err := hex.DecodeString(nonce)
	require.NoError(t, err)

	rawSig := ed25519.Sign(priv, nonceBytes)
	require.Error(t, VerifyChallengeV2Signature(pub, nonce, "https://home.example", rawSig))
}

// ----- audience normalization -----

func TestNormalizeAudience_Cases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://Home.Example.com", "https://home.example.com"},
		{"https://home.example.com/", "https://home.example.com"},
		{"https://home.example.com/path/ignored?q=1#frag", "https://home.example.com"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://home.example.com:443", "https://home.example.com"},
		{"http://home.example.com:80", "http://home.example.com"},
		{"  https://home.example.com  ", "https://home.example.com"},
		{"", ""},
		{"app://localhost", ""}, // non-http(s) schemes have no API origin meaning
		{"not-a-url", ""},
		{"https://", ""},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, NormalizeAudience(tc.in), "input=%q", tc.in)
	}
}
