package api

import (
	"encoding/base64"
	"encoding/hex"
	"net/http"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/transparency"

	"github.com/go-chi/chi/v5"
)

// transparencyHandler handles the /api/transparency endpoint group.
type transparencyHandler struct {
	svc *transparency.TransparencyService
}

// TransparencyRoutes returns a chi.Router for the transparency log API.
// All routes require JWT authentication.
//
// Route map:
//
//	GET /verify — return inclusion proofs for a given Ed25519 public key
func TransparencyRoutes(svc *transparency.TransparencyService, store db.Store, jwtSecret string) chi.Router {
	r := chi.NewRouter()
	h := &transparencyHandler{svc: svc}
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/verify", h.verify)
	return r
}

// verify handles GET /api/transparency/verify?pubkey=<hex>.
//
// pubkey must be a 32-byte Ed25519 public key encoded as lowercase hex.
// Returns a JSON body with:
//
//	{
//	  "entries": [ ...TransparencyLogEntry... ],
//	  "proofs":  [ ...MerkleInclusionProof... ],
//	  "treeHead": { "size": N, "root": "<hex>", "signature": "<hex>" }
//	}
//
// When no entries exist for the given key, "entries" and "proofs" are empty arrays.
func (h *transparencyHandler) verify(w http.ResponseWriter, r *http.Request) {
	pubkeyHex := r.URL.Query().Get("pubkey")
	if pubkeyHex == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pubkey query parameter is required"})
		return
	}

	pubKey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pubkey must be valid hex"})
		return
	}

	// Ed25519 public keys are exactly 32 bytes.
	if len(pubKey) != 32 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pubkey must be 32 bytes (64 hex chars)"})
		return
	}

	proof, err := h.svc.GetProof(r.Context(), pubKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve proof"})
		return
	}

	// Ensure slices are never null in the JSON response — always emit arrays.
	entries := proof.Entries
	if entries == nil {
		entries = make([]models.TransparencyLogEntry, 0)
	}
	proofs := proof.Proofs
	if proofs == nil {
		proofs = make([]models.MerkleInclusionProof, 0)
	}

	// Populate wire-format fields on each entry. EntryCBOR is tagged json:"-" on the
	// struct so the raw bytes are never marshaled implicitly. The client's verify()
	// reads entry.entryCbor (standard base64) to compute the Merkle leaf hash.
	for i := range entries {
		entries[i].EntryCBORB64 = base64.StdEncoding.EncodeToString(entries[i].EntryCBOR)
		entries[i].LeafHashHex = hex.EncodeToString(entries[i].LeafHash)
		entries[i].LogSigB64 = base64.StdEncoding.EncodeToString(entries[i].LogSig)
		entries[i].UserPubKeyHex = hex.EncodeToString(entries[i].UserPubKey)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"proofs":  proofs,
		"treeHead": map[string]interface{}{
			"size": proof.TreeSize,
			"root": hex.EncodeToString(proof.RootHash[:]),
		},
	})
}
