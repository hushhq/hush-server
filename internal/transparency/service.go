package transparency

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hushhq/hush-server/internal/models"
)

// Broadcaster sends WS messages to connected users. Implemented by ws.Hub.
type Broadcaster interface {
	BroadcastToUser(userID string, message []byte)
}

// AppendResult is returned by AppendEntry with proof data the caller can
// forward to the client for immediate verification.
type AppendResult struct {
	LeafIndex    uint64
	TreeSize     uint64
	AuditPath    [][32]byte
	RootHash     [32]byte
	LogSignature []byte
}

// ProofResponse is returned by GetProof — the full inclusion evidence for
// a given public key's history.
type ProofResponse struct {
	Entries  []models.TransparencyLogEntry
	Proofs   []models.MerkleInclusionProof
	TreeSize uint64
	RootHash [32]byte
}

// TransparencyService orchestrates appending entries to the Merkle tree,
// countersigning them, persisting to the DB, and generating inclusion proofs.
//
// The in-memory MerkleTree is recovered from the DB on construction via
// RecoverFromDB. It is protected by a mutex so concurrent handler goroutines
// can safely call AppendEntry.
type TransparencyService struct {
	mu          sync.Mutex
	tree        *MerkleTree
	signer      *LogSigner
	store       TransparencyStore
	broadcaster Broadcaster
}

// NewTransparencyService creates a service and recovers the Merkle tree
// fringe from the latest DB tree head. If the log is empty the tree starts
// at size 0.
func NewTransparencyService(store TransparencyStore, signer *LogSigner) (*TransparencyService, error) {
	svc := &TransparencyService{
		tree:   NewMerkleTree(),
		signer: signer,
		store:  store,
	}
	if err := svc.RecoverFromDB(context.Background()); err != nil {
		return nil, err
	}
	return svc, nil
}

// RecoverFromDB loads the latest persisted tree head and reconstructs the
// in-memory tree fringe so new entries can be appended without a full replay.
func (s *TransparencyService) RecoverFromDB(ctx context.Context) error {
	head, err := s.store.GetLatestTreeHead(ctx)
	if err != nil {
		return fmt.Errorf("transparency: recover from DB: %w", err)
	}
	if head == nil {
		// Empty log — start fresh.
		return nil
	}
	fringe := fringeFromBytes(head.Fringe)
	s.tree = FromFringe(fringe, head.TreeSize)

	// Rehydrate leaf hashes so Proof() can rebuild the full tree.
	// Without this, proofs for entries appended before this process started
	// would use zero-valued hashes and always fail verification.
	leafHashes, err := s.store.GetAllLeafHashes(ctx)
	if err != nil {
		return fmt.Errorf("transparency: load leaf hashes: %w", err)
	}
	s.tree.SetLeaves(leafHashes)
	return nil
}

// AppendEntry verifies the user's signature (when present), appends the entry
// to the Merkle tree, countersigns, persists both the entry and the new tree
// head, and returns an AppendResult for immediate client verification.
//
// When UserSignature is nil or empty the signature verification step is skipped.
// This is the MVP mode — client-side signing is added in T.1 Plan 03. A non-nil,
// non-empty UserSignature that fails verification returns an error without
// modifying tree state.
func (s *TransparencyService) AppendEntry(ctx context.Context, entry *LogEntry) (*AppendResult, error) {
	// 1. Verify user signature over fields 1-4 (skip when nil/empty for MVP).
	if len(entry.UserSignature) > 0 {
		payload, err := entry.SerializeForUserSign()
		if err != nil {
			return nil, fmt.Errorf("transparency: serialize for verify: %w", err)
		}
		if !ed25519.Verify(entry.UserPublicKey, payload, entry.UserSignature) {
			return nil, fmt.Errorf("transparency: user signature verification failed")
		}
	}

	// 2. Compute full CBOR and leaf hash.
	cborBytes, err := entry.MarshalCBOR()
	if err != nil {
		return nil, fmt.Errorf("transparency: marshal entry: %w", err)
	}
	leafH, err := entry.LeafHash()
	if err != nil {
		return nil, fmt.Errorf("transparency: compute leaf hash: %w", err)
	}

	// 3. Append to tree under lock.
	s.mu.Lock()
	defer s.mu.Unlock()

	leafIndex := s.tree.Append(cborBytes)
	root := s.tree.Root()
	treeSize := s.tree.Size()

	// 4. Generate inclusion proof while tree is still consistent.
	auditPath, err := s.tree.Proof(leafIndex)
	if err != nil {
		// Roll back is not needed: the tree will recover from DB on next restart.
		log.Printf("transparency: proof generation failed for leaf %d: %v", leafIndex, err)
		auditPath = nil
	}

	// 5. Countersign (entry CBOR + leafIndex + root).
	logSig, err := s.signer.Countersign(entry, leafIndex, root)
	if err != nil {
		return nil, fmt.Errorf("transparency: countersign: %w", err)
	}

	// 6. Persist entry and tree head.
	if err := s.store.InsertLogEntry(ctx, leafIndex, entry, cborBytes, leafH[:], logSig); err != nil {
		return nil, fmt.Errorf("transparency: persist entry: %w", err)
	}

	fringeBytes := fringeToBytes(s.tree.Fringe())
	headSig := s.signer.Sign(append(root[:], fringeBytes...))
	if err := s.store.InsertTreeHead(ctx, treeSize, root[:], fringeBytes, headSig); err != nil {
		// Non-fatal: tree state is in memory; log and continue. On restart the
		// previous head will be recovered (one-entry gap in fringe is safe).
		log.Printf("transparency: failed to persist tree head (size=%d): %v", treeSize, err)
	}

	return &AppendResult{
		LeafIndex:    leafIndex,
		TreeSize:     treeSize,
		AuditPath:    auditPath,
		RootHash:     root,
		LogSignature: logSig,
	}, nil
}

// GetProof returns inclusion proofs for all log entries belonging to pubKey.
// Each entry has its own MerkleInclusionProof. The proofs reference the
// current tree size and root so the client can verify freshness.
func (s *TransparencyService) GetProof(ctx context.Context, pubKey []byte) (*ProofResponse, error) {
	entries, err := s.store.GetLogEntriesByPubKey(ctx, pubKey)
	if err != nil {
		return nil, fmt.Errorf("transparency: get entries: %w", err)
	}

	s.mu.Lock()
	root := s.tree.Root()
	treeSize := s.tree.Size()
	s.mu.Unlock()

	proofs := make([]models.MerkleInclusionProof, 0, len(entries))
	for _, e := range entries {
		s.mu.Lock()
		auditPath, proofErr := s.tree.Proof(e.LeafIndex)
		s.mu.Unlock()

		if proofErr != nil {
			// Return what we have; the caller can retry stale entries.
			log.Printf("transparency: proof unavailable for leaf %d: %v", e.LeafIndex, proofErr)
			continue
		}

		// Convert [][32]byte to wire-format hex strings for the client.
		// The client's verifyInclusion() passes each sibling through hexToBytes(),
		// so all audit path hashes must be lowercase hex, not base64.
		pathHex := make([]string, len(auditPath))
		pathBytes := make([][]byte, len(auditPath))
		for i, h := range auditPath {
			hCopy := h
			pathBytes[i] = hCopy[:]
			pathHex[i] = hex.EncodeToString(hCopy[:])
		}

		// Countersign the proof using original stored CBOR + current root.
		logSig, sigErr := s.countersignProof(e, root)
		if sigErr != nil {
			log.Printf("transparency: proof countersign failed for leaf %d: %v", e.LeafIndex, sigErr)
			logSig = e.LogSig // Fall back to the original entry log signature.
		}

		proofs = append(proofs, models.MerkleInclusionProof{
			LeafIndex:       e.LeafIndex,
			TreeSize:        treeSize,
			AuditPath:       pathBytes,
			RootHash:        root[:],
			LogSig:          logSig,
			AuditPathHex:    pathHex,
			RootHashHex:     hex.EncodeToString(root[:]),
			LogSignatureB64: base64.StdEncoding.EncodeToString(logSig),
		})
	}

	return &ProofResponse{
		Entries:  entries,
		Proofs:   proofs,
		TreeSize: treeSize,
		RootHash: root,
	}, nil
}

// countersignProof signs the proof data using the log's private key so the
// client can verify the proof was generated by the trusted log operator.
// Uses the original stored CBOR bytes — NOT a reconstructed LogEntry — to
// guarantee the signed message matches what the client receives.
func (s *TransparencyService) countersignProof(
	entry models.TransparencyLogEntry,
	root [32]byte,
) ([]byte, error) {
	idxBytes := leafIndexBigEndian(entry.LeafIndex)
	msg := make([]byte, 0, len(entry.EntryCBOR)+8+32)
	msg = append(msg, entry.EntryCBOR...)
	msg = append(msg, idxBytes...)
	msg = append(msg, root[:]...)
	return s.signer.Sign(msg), nil
}

// TreeSize returns the current number of leaves in the tree.
// Safe to call without the lock since uint64 reads are atomic on all
// supported architectures, but callers needing consistency should use
// the lock explicitly.
func (s *TransparencyService) TreeSize() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tree.Size()
}

// RootHash returns the current Merkle root.
func (s *TransparencyService) RootHash() [32]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tree.Root()
}

// SignerPublicKey returns the log's Ed25519 public key for inclusion in
// the instance handshake response.
func (s *TransparencyService) SignerPublicKey() ed25519.PublicKey {
	return s.signer.PublicKey()
}

// SetBroadcaster wires the WS hub for transparency.key_change notifications.
// Called once in main.go after the hub is created. Nil-safe: the service works
// without a broadcaster; key_change events are simply not sent.
func (s *TransparencyService) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}

// broadcastKeyChange notifies the user via WS that a transparency entry was
// appended for their public key. The client uses this to trigger re-verification.
func (s *TransparencyService) broadcastKeyChange(userID string, entry *LogEntry, result *AppendResult) {
	if s.broadcaster == nil || userID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"type":      "transparency.key_change",
		"operation": string(entry.OperationType),
		"leafIndex": result.LeafIndex,
		"treeRoot":  hex.EncodeToString(result.RootHash[:]),
	})
	if err != nil {
		log.Printf("transparency: marshal key_change event: %v", err)
		return
	}
	s.broadcaster.BroadcastToUser(userID, payload)
}

// AppendEntrySkipSig appends an entry without requiring a user signature.
// Used by server handlers in MVP mode (T.1 Plan 02) before client-side signing
// is implemented in Plan 03. The UserSignature field of the entry is set to nil.
func (s *TransparencyService) AppendEntrySkipSig(ctx context.Context, entry *LogEntry) error {
	entry.UserSignature = nil
	_, err := s.AppendEntry(ctx, entry)
	return err
}

// AppendAndNotify appends an entry (skipping user signature in MVP mode) and
// broadcasts a transparency.key_change WS event to the specified user.
// Used by handlers that know the user ID and want fire-and-forget notification.
func (s *TransparencyService) AppendAndNotify(ctx context.Context, entry *LogEntry, userID string) error {
	entry.UserSignature = nil
	result, err := s.AppendEntry(ctx, entry)
	if err != nil {
		return err
	}
	s.broadcastKeyChange(userID, entry, result)
	return nil
}

// nowUnix is a variable to allow time injection in tests.
var nowUnix = func() int64 { return time.Now().Unix() }
