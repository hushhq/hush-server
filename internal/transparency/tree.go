// Package transparency implements an append-only binary Merkle tree and the
// supporting types for the Hush transparency log (RFC 6962 / CT model).
package transparency

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
)

// LeafHash computes SHA-256(0x00 || data) per RFC 6962 §2.1.
// Exported so tests and the entry package can compute reference values.
func LeafHash(data []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(data)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// NodeHash computes SHA-256(0x01 || left || right) per RFC 6962 §2.1.
// Exported for test validation.
func NodeHash(left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left[:])
	h.Write(right[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MerkleTree is an in-memory, append-only binary Merkle tree.
// Each leaf stores the hash of raw leaf data. The fringe (right-edge sibling
// hashes) enables O(log N) append without recomputing the full tree.
type MerkleTree struct {
	leaves [][32]byte // leaf hashes, indexed by leaf index
	fringe [][32]byte // right-edge hashes for incremental root computation
	size   uint64
}

// NewMerkleTree returns an empty MerkleTree.
func NewMerkleTree() *MerkleTree {
	return &MerkleTree{}
}

// FromFringe reconstructs a MerkleTree state from a stored fringe and size.
// This is used on startup to recover from the latest persisted tree head without
// re-reading all log entries.
func FromFringe(fringe [][32]byte, size uint64) *MerkleTree {
	t := &MerkleTree{size: size}
	t.fringe = make([][32]byte, len(fringe))
	copy(t.fringe, fringe)
	return t
}

// SetLeaves populates the stored leaf hashes for a fringe-recovered tree.
// Must be called before Proof() when the tree was loaded via FromFringe.
// The hashes must be in leaf-index order and len(hashes) must equal Size().
func (t *MerkleTree) SetLeaves(hashes [][32]byte) {
	t.leaves = make([][32]byte, len(hashes))
	copy(t.leaves, hashes)
}

// Append adds a new leaf (raw data bytes) and returns its 0-based index.
// Each call is O(log N).
func (t *MerkleTree) Append(leafData []byte) uint64 {
	idx := t.size
	h := LeafHash(leafData)
	t.leaves = append(t.leaves, h)

	// Update fringe using the CT incremental computation.
	// Walk up the perfect subtrees on the right edge.
	carry := h
	n := idx
	for n > 0 && n%2 == 1 {
		// n is odd - the current perfect subtree can be merged with its sibling
		// on the fringe
		carry = NodeHash(t.fringe[len(t.fringe)-1], carry)
		t.fringe = t.fringe[:len(t.fringe)-1]
		n /= 2
	}
	t.fringe = append(t.fringe, carry)

	t.size++
	return idx
}

// Root returns the current Merkle root. Returns the zero value for an empty tree.
func (t *MerkleTree) Root() [32]byte {
	if t.size == 0 {
		return [32]byte{}
	}
	if len(t.fringe) == 1 {
		return t.fringe[0]
	}
	// Merge fringe hashes right-to-left (the right-most fringe represents the
	// most recent perfect sub-tree and gets hashed last as the right child).
	acc := t.fringe[len(t.fringe)-1]
	for i := len(t.fringe) - 2; i >= 0; i-- {
		acc = NodeHash(t.fringe[i], acc)
	}
	return acc
}

// Size returns the number of leaves in the tree.
func (t *MerkleTree) Size() uint64 {
	return t.size
}

// Fringe returns a copy of the right-edge sibling hashes needed to reconstruct
// the tree state after persistence.
func (t *MerkleTree) Fringe() [][32]byte {
	out := make([][32]byte, len(t.fringe))
	copy(out, t.fringe)
	return out
}

// Proof returns the inclusion audit path for the leaf at leafIndex.
// The audit path contains the sibling hashes from the leaf level up to the
// root - enough to recompute the root without the other leaves.
// Returns an error if leafIndex >= Size().
func (t *MerkleTree) Proof(leafIndex uint64) ([][32]byte, error) {
	if leafIndex >= t.size {
		return nil, errors.New("transparency: leaf index out of range")
	}

	// Build a level-by-level slice of hashes by first computing the entire
	// tree from stored leaf hashes.  This is O(N) but only needed for proof
	// generation, not for append or root queries.
	levels := buildTree(t.leaves, t.size)
	if len(levels) == 0 {
		return nil, errors.New("transparency: tree is empty")
	}

	var auditPath [][32]byte
	idx := leafIndex
	for level := 0; level < len(levels)-1; level++ {
		row := levels[level]
		if idx%2 == 0 {
			if idx+1 < uint64(len(row)) {
				auditPath = append(auditPath, row[idx+1])
			}
			// No sibling (promoted): skip this level entirely.
		} else {
			// Current node is right child - sibling is left child.
			auditPath = append(auditPath, row[idx-1])
		}
		idx /= 2
	}
	return auditPath, nil
}

// VerifyProof verifies an inclusion proof for leafData at leafIndex in a tree
// of treeSize leaves with the given expectedRoot.
func VerifyProof(leafData []byte, leafIndex, treeSize uint64, auditPath [][32]byte, expectedRoot [32]byte) bool {
	if leafIndex >= treeSize {
		return false
	}

	current := LeafHash(leafData)
	idx := leafIndex
	pathIdx := 0
	n := treeSize

	for n > 1 {
		if idx%2 == 0 {
			if idx+1 < n {
				if pathIdx >= len(auditPath) {
					return false
				}
				current = NodeHash(current, auditPath[pathIdx])
				pathIdx++
			}
			// Else: promoted - no sibling, no hash, no path element consumed.
		} else {
			if pathIdx >= len(auditPath) {
				return false
			}
			current = NodeHash(auditPath[pathIdx], current)
			pathIdx++
		}
		idx /= 2
		n = (n + 1) / 2
	}

	return pathIdx == len(auditPath) && current == expectedRoot
}

// buildTree computes all levels of the Merkle tree from leaf hashes.
// levels[0] is the leaf level; levels[len-1] is the root level (single hash).
func buildTree(leaves [][32]byte, size uint64) [][][32]byte {
	if size == 0 {
		return nil
	}
	current := make([][32]byte, size)
	copy(current, leaves)

	var levels [][][32]byte
	levels = append(levels, current)

	for len(current) > 1 {
		var next [][32]byte
		for i := 0; i+1 < len(current); i += 2 {
			next = append(next, NodeHash(current[i], current[i+1]))
		}
		if len(current)%2 == 1 {
			// Odd node: promote as-is to the next level.
			next = append(next, current[len(current)-1])
		}
		levels = append(levels, next)
		current = next
	}
	return levels
}

// treeDepth returns the number of hash levels needed for an N-leaf proof.
// This is ceil(log2(N)) for N > 1, and 0 for N == 1.
func treeDepth(size uint64) int {
	if size <= 1 {
		return 0
	}
	return bits.Len64(size - 1)
}

// fringeToBytes serializes a fringe slice to a flat byte array for DB storage.
// Format: each [32]byte hash concatenated. Returns nil for empty fringe.
func fringeToBytes(fringe [][32]byte) []byte {
	if len(fringe) == 0 {
		return nil
	}
	out := make([]byte, len(fringe)*32)
	for i, h := range fringe {
		copy(out[i*32:], h[:])
	}
	return out
}

// fringeFromBytes deserializes a fringe from flat byte array (inverse of fringeToBytes).
func fringeFromBytes(b []byte) [][32]byte {
	if len(b)%32 != 0 || len(b) == 0 {
		return nil
	}
	out := make([][32]byte, len(b)/32)
	for i := range out {
		copy(out[i][:], b[i*32:(i+1)*32])
	}
	return out
}

// leafIndexBigEndian encodes a uint64 leaf index as 8 big-endian bytes.
// Used as part of the log signature payload.
func leafIndexBigEndian(idx uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, idx)
	return b
}
