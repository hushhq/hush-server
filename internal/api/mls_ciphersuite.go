package api

import (
	"fmt"
	"net/http"

	"github.com/hushhq/hush-server/internal/version"
)

// validateMLSCiphersuiteHTTP enforces the X-Wing ciphersuite contract on an
// HTTP MLS write endpoint. It returns true when the request explicitly declared
// version.CurrentMLSCiphersuite. Otherwise it writes a 400 response describing
// the mismatch and returns false; the caller MUST stop processing the request
// in that case.
//
// The server cannot parse opaque MLS blobs, so this is a client-attested check.
// It does NOT replace the server-side stamping done by the db accessors: every
// row written to the MLS state tables is still stamped with
// version.CurrentMLSCiphersuite by the accessor regardless of what the client
// claimed.  The purpose of this check is to fail closed when a legacy client
// tries to upload MLS bytes generated under a different suite.
//
// A value of zero is treated as "missing" rather than "mismatched" so the
// error message tells the client which case it hit.
func validateMLSCiphersuiteHTTP(w http.ResponseWriter, declared int) bool {
	if declared == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf(
				"ciphersuite is required; current server suite is %d",
				version.CurrentMLSCiphersuite,
			),
		})
		return false
	}
	if declared != version.CurrentMLSCiphersuite {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":              "mls_ciphersuite_mismatch",
			"declared":           declared,
			"current_ciphersuite": version.CurrentMLSCiphersuite,
		})
		return false
	}
	return true
}
