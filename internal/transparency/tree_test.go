package transparency_test

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hushhq/hush-server/internal/transparency"
)

// leafHash computes SHA-256(0x00 || data) — RFC 6962 §2.1 leaf prefix.
func expectedLeafHash(data []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(data)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// nodeHash computes SHA-256(0x01 || left || right) — RFC 6962 §2.1 node prefix.
func expectedNodeHash(left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left[:])
	h.Write(right[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// TestLeafHash verifies SHA-256(0x00 || data) leaf hashing.
func TestLeafHash(t *testing.T) {
	data := []byte("hello transparency log")
	got := transparency.LeafHash(data)
	want := expectedLeafHash(data)
	require.Equal(t, want, got)
}

// TestNodeHash verifies SHA-256(0x01 || left || right) node hashing.
func TestNodeHash(t *testing.T) {
	left := expectedLeafHash([]byte("left"))
	right := expectedLeafHash([]byte("right"))
	got := transparency.NodeHash(left, right)
	want := expectedNodeHash(left, right)
	require.Equal(t, want, got)
}

// TestAppendAndRoot checks root computation for 1 and 2 leaves.
func TestAppendAndRoot(t *testing.T) {
	tree := transparency.NewMerkleTree()

	leaf0Data := []byte("leaf-zero")
	idx0 := tree.Append(leaf0Data)
	require.Equal(t, uint64(0), idx0)
	require.Equal(t, uint64(1), tree.Size())

	// Root of 1 leaf = leafHash(leaf0)
	want1 := expectedLeafHash(leaf0Data)
	require.Equal(t, want1, tree.Root())

	// Append second leaf
	leaf1Data := []byte("leaf-one")
	idx1 := tree.Append(leaf1Data)
	require.Equal(t, uint64(1), idx1)
	require.Equal(t, uint64(2), tree.Size())

	// Root of 2 leaves = nodeHash(leafHash(l0), leafHash(l1))
	l0 := expectedLeafHash(leaf0Data)
	l1 := expectedLeafHash(leaf1Data)
	want2 := expectedNodeHash(l0, l1)
	require.Equal(t, want2, tree.Root())
}

// TestInclusionProof verifies audit path generation for an 8-leaf tree.
func TestInclusionProof(t *testing.T) {
	tree := transparency.NewMerkleTree()
	leaves := make([][]byte, 8)
	for i := range leaves {
		leaves[i] = []byte{byte(i)}
		tree.Append(leaves[i])
	}

	// Proof for leaf index 3 must reconstruct to tree.Root()
	auditPath, err := tree.Proof(3)
	require.NoError(t, err)

	// auditPath should have log2(8)=3 siblings
	require.Len(t, auditPath, 3)

	// Verify the path reconstructs the root
	root := tree.Root()
	ok := transparency.VerifyProof(leaves[3], 3, tree.Size(), auditPath, root)
	require.True(t, ok, "inclusion proof should verify to current root")
}

// TestVerifyProof validates VerifyProof returns false for tampered data.
func TestVerifyProof(t *testing.T) {
	tree := transparency.NewMerkleTree()
	var leafData [][]byte
	for i := 0; i < 8; i++ {
		d := []byte{byte(i), byte(i * 2)}
		leafData = append(leafData, d)
		tree.Append(d)
	}

	auditPath, err := tree.Proof(5)
	require.NoError(t, err)
	root := tree.Root()

	// Valid proof
	require.True(t, transparency.VerifyProof(leafData[5], 5, tree.Size(), auditPath, root))

	// Tampered leaf data — must fail
	require.False(t, transparency.VerifyProof([]byte("tampered"), 5, tree.Size(), auditPath, root))

	// Wrong leaf index — must fail
	require.False(t, transparency.VerifyProof(leafData[5], 4, tree.Size(), auditPath, root))

	// Tampered root — must fail
	var badRoot [32]byte
	copy(badRoot[:], root[:])
	badRoot[0] ^= 0xFF
	require.False(t, transparency.VerifyProof(leafData[5], 5, tree.Size(), auditPath, badRoot))
}

// TestFringeStorage verifies Fringe() and FromFringe() round-trip.
func TestFringeStorage(t *testing.T) {
	tree := transparency.NewMerkleTree()
	for i := 0; i < 7; i++ {
		tree.Append([]byte{byte(i)})
	}

	fringe := tree.Fringe()
	size := tree.Size()
	root := tree.Root()

	// Reconstruct from fringe
	tree2 := transparency.FromFringe(fringe, size)
	require.Equal(t, size, tree2.Size())
	require.Equal(t, root, tree2.Root())

	// Appending to the recovered tree should produce the same root as the original
	newData := []byte("new-leaf")
	tree.Append(newData)
	tree2.Append(newData)
	require.Equal(t, tree.Root(), tree2.Root())
}

// TestInclusionProofOddTrees verifies proofs for trees whose size is not a
// power of two. The promoted (unpaired) leaf at each level must still verify.
func TestInclusionProofOddTrees(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int
		index uint64
	}{
		{"3-leaf tree, promoted leaf 2", 3, 2},
		{"3-leaf tree, leaf 0", 3, 0},
		{"3-leaf tree, leaf 1", 3, 1},
		{"5-leaf tree, promoted leaf 4", 5, 4},
		{"5-leaf tree, leaf 2", 5, 2},
		{"7-leaf tree, promoted leaf 6", 7, 6},
		{"7-leaf tree, leaf 3", 7, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := transparency.NewMerkleTree()
			leaves := make([][]byte, tc.size)
			for i := range leaves {
				leaves[i] = []byte{byte(i), byte(i + 10)}
				tree.Append(leaves[i])
			}

			auditPath, err := tree.Proof(tc.index)
			require.NoError(t, err)

			root := tree.Root()
			ok := transparency.VerifyProof(leaves[tc.index], tc.index, tree.Size(), auditPath, root)
			require.True(t, ok, "proof for leaf %d in %d-leaf tree must verify", tc.index, tc.size)
		})
	}
}

// TestOutOfBoundsProof verifies Proof returns an error for invalid indices.
func TestOutOfBoundsProof(t *testing.T) {
	tree := transparency.NewMerkleTree()
	tree.Append([]byte("only-leaf"))

	_, err := tree.Proof(1) // index 1 does not exist in a 1-leaf tree
	require.Error(t, err)

	_, err = tree.Proof(0) // index 0 is valid
	require.NoError(t, err)
}
