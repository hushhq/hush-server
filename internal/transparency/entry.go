package transparency

import (
	"crypto/sha256"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// cborEnc is the package-level deterministic CBOR encoder.
// CoreDetEncOptions enforces Core Deterministic Encoding (RFC 7049 §3.9 /
// RFC 8949 §4.2.1): sorted keys, smallest-width integers, no indefinite-length items.
var cborEnc cbor.EncMode

func init() {
	var err error
	cborEnc, err = cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(fmt.Sprintf("transparency: failed to create CBOR encoder: %v", err))
	}
}

// OperationType enumerates the allowed key lifecycle operations.
type OperationType = string

const (
	OpRegister        OperationType = "register"
	OpDeviceAdd       OperationType = "device_add"
	OpDeviceRevoke    OperationType = "device_revoke"
	OpKeyPackage      OperationType = "keypackage"
	OpMLSCredential   OperationType = "mls_credential"
	OpAccountRecovery OperationType = "account_recovery"
)

// logEntryFields1To4 is a wire-format struct carrying only the user-signed
// fields. keyasint tags produce integer CBOR map keys (1, 2, 3, 4) for
// deterministic, compact encoding.
type logEntryFields1To4 struct {
	OperationType string `cbor:"1,keyasint"`
	UserPublicKey []byte `cbor:"2,keyasint"`
	SubjectKey    []byte `cbor:"3,keyasint"`
	Timestamp     int64  `cbor:"4,keyasint"`
}

// logEntryFull is the complete wire-format struct including the user's signature.
type logEntryFull struct {
	OperationType string `cbor:"1,keyasint"`
	UserPublicKey []byte `cbor:"2,keyasint"`
	SubjectKey    []byte `cbor:"3,keyasint"`
	Timestamp     int64  `cbor:"4,keyasint"`
	UserSignature []byte `cbor:"5,keyasint"`
}

// LogEntry represents a single entry in the transparency log.
// Field numbering matches the CBOR wire format (1-indexed integer keys).
//
// Signing model:
//   - Fields 1-4 form the payload the user signs (SerializeForUserSign).
//   - UserSignature (field 5) is the Ed25519 signature by the user's root key.
//   - The log countersigns the full CBOR (fields 1-5) + leaf index + root hash.
type LogEntry struct {
	// Field 1: type of key operation.
	OperationType OperationType
	// Field 2: 32-byte Ed25519 root public key of the acting user.
	UserPublicKey []byte
	// Field 3: device public key, KeyPackage reference hash, or nil.
	SubjectKey []byte
	// Field 4: Unix seconds of the event.
	Timestamp int64
	// Field 5: Ed25519 signature by UserPublicKey over SerializeForUserSign().
	UserSignature []byte
}

// SerializeForUserSign returns the deterministic CBOR encoding of fields 1-4.
// This is the payload the user signs before submitting the entry.
func (e *LogEntry) SerializeForUserSign() ([]byte, error) {
	payload := logEntryFields1To4{
		OperationType: e.OperationType,
		UserPublicKey: e.UserPublicKey,
		SubjectKey:    e.SubjectKey,
		Timestamp:     e.Timestamp,
	}
	b, err := cborEnc.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("transparency: serialize for user sign: %w", err)
	}
	return b, nil
}

// MarshalCBOR returns the deterministic CBOR encoding of all five fields
// (including UserSignature). This is the byte sequence hashed as the leaf.
func (e *LogEntry) MarshalCBOR() ([]byte, error) {
	full := logEntryFull{
		OperationType: e.OperationType,
		UserPublicKey: e.UserPublicKey,
		SubjectKey:    e.SubjectKey,
		Timestamp:     e.Timestamp,
		UserSignature: e.UserSignature,
	}
	b, err := cborEnc.Marshal(full)
	if err != nil {
		return nil, fmt.Errorf("transparency: marshal CBOR: %w", err)
	}
	return b, nil
}

// LeafHash returns leafHash(MarshalCBOR()) - the 32-byte value stored in the
// Merkle tree for this entry.
func (e *LogEntry) LeafHash() ([32]byte, error) {
	cborBytes, err := e.MarshalCBOR()
	if err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(cborBytes)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
