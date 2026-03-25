package transparency

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
)

// LogSigner holds the Ed25519 signing keypair for the transparency log.
// It countersigns each log entry after the entry is appended to the tree,
// proving that the log operator witnessed the inclusion.
type LogSigner struct {
	privKey ed25519.PrivateKey
	pubKey  ed25519.PublicKey
}

// NewLogSignerFromKey constructs a LogSigner from an existing Ed25519 private key.
// Used in tests to inject a known keypair.
func NewLogSignerFromKey(privKey ed25519.PrivateKey) *LogSigner {
	return &LogSigner{
		privKey: privKey,
		pubKey:  privKey.Public().(ed25519.PublicKey),
	}
}

// LoadLogSignerFromEnv reads the transparency log signing keypair from the
// TRANSPARENCY_LOG_PRIVATE_KEY environment variable (32-byte seed, hex-encoded).
//
// Behavior:
//   - Variable present: parse and return keypair.
//   - Variable absent + PRODUCTION not set: generate an ephemeral key and log a warning.
//   - Variable absent + PRODUCTION set: return an error (safe default).
func LoadLogSignerFromEnv() (*LogSigner, error) {
	hexSeed := os.Getenv("TRANSPARENCY_LOG_PRIVATE_KEY")
	if hexSeed == "" {
		prod := os.Getenv("PRODUCTION")
		if prod == "1" || prod == "true" || prod == "yes" {
			return nil, errors.New(
				"transparency: TRANSPARENCY_LOG_PRIVATE_KEY must be set in production mode",
			)
		}
		// Dev/test: generate ephemeral key so the service still starts.
		log.Println("WARN: transparency log key is ephemeral — set TRANSPARENCY_LOG_PRIVATE_KEY for persistent verification")
		return generateEphemeralSigner()
	}

	seed, err := hex.DecodeString(hexSeed)
	if err != nil {
		return nil, fmt.Errorf("transparency: TRANSPARENCY_LOG_PRIVATE_KEY is not valid hex: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf(
			"transparency: TRANSPARENCY_LOG_PRIVATE_KEY must be %d bytes (got %d)",
			ed25519.SeedSize, len(seed),
		)
	}

	privKey := ed25519.NewKeyFromSeed(seed)
	return &LogSigner{
		privKey: privKey,
		pubKey:  privKey.Public().(ed25519.PublicKey),
	}, nil
}

// Sign returns an Ed25519 signature over data using the log's private key.
func (s *LogSigner) Sign(data []byte) []byte {
	return ed25519.Sign(s.privKey, data)
}

// PublicKey returns the log's Ed25519 public key. Clients use this key to
// verify log countersignatures received via the handshake response.
func (s *LogSigner) PublicKey() ed25519.PublicKey {
	return s.pubKey
}

// Countersign signs CBOR(entry fields 1-5) || leafIndexBE8 || rootHash[32].
// The signature proves that the log operator included this entry at the given
// position under the given root at the time of signing.
func (s *LogSigner) Countersign(entry *LogEntry, leafIndex uint64, root [32]byte) ([]byte, error) {
	fullCBOR, err := entry.MarshalCBOR()
	if err != nil {
		return nil, fmt.Errorf("transparency: countersign marshal: %w", err)
	}

	idxBytes := leafIndexBigEndian(leafIndex)
	msg := make([]byte, 0, len(fullCBOR)+8+32)
	msg = append(msg, fullCBOR...)
	msg = append(msg, idxBytes...)
	msg = append(msg, root[:]...)

	return ed25519.Sign(s.privKey, msg), nil
}

// generateEphemeralSigner creates a fresh random Ed25519 keypair for dev use.
func generateEphemeralSigner() (*LogSigner, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("transparency: generate ephemeral key: %w", err)
	}
	return &LogSigner{privKey: priv, pubKey: pub}, nil
}

// NewEphemeralLogSigner generates a fresh random Ed25519 keypair.
// Exported for use in tests that need a valid LogSigner without environment variables.
func NewEphemeralLogSigner() (*LogSigner, error) {
	return generateEphemeralSigner()
}
