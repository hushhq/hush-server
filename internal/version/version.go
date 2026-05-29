// Package version provides build-time injectable version variables and protocol constants.
//
// ServerVersion, APIVersion, and MinClientVersion can be overridden at build time via ldflags:
//
//	go build -ldflags "-X github.com/hushhq/hush-server/internal/version.ServerVersion=1.0.0 \
//	  -X github.com/hushhq/hush-server/internal/version.APIVersion=v2 \
//	  -X github.com/hushhq/hush-server/internal/version.MinClientVersion=0.5.0"
package version

// ServerVersion is the server release version. Defaults to "dev" for local builds.
// Override via: -ldflags "-X github.com/hushhq/hush-server/internal/version.ServerVersion=x.y.z"
var ServerVersion = "dev"

// APIVersion is the API version prefix. Defaults to "v1".
// Override via: -ldflags "-X github.com/hushhq/hush-server/internal/version.APIVersion=v2"
var APIVersion = "v1"

// MinClientVersion is the minimum client version required to connect. Defaults to "0.0.0".
// Override via: -ldflags "-X github.com/hushhq/hush-server/internal/version.MinClientVersion=x.y.z"
//
// DEPRECATED (HUSHHQ-83 phase 1): JSON consumers should read the canonical
// `min_compatible_client_version` field on the handshake response. The
// `min_client_version` field is kept in parallel only for backward
// compatibility with already-deployed clients.
//
// Sunset plan: HUSHHQ-83 phase 4 wires the client to read the canonical
// field. One minor server release after that lands, the legacy
// `min_client_version` JSON field is dropped from the handshake response
// (this Go variable can stay because it still backs the canonical field;
// only the duplicated JSON output goes away).
var MinClientVersion = "0.0.0"

// CurrentDBSchemaVersion is the highest schema_migrations.version number this
// binary has been compiled with. The boot-time DB schema guardrail (HUSHHQ-83
// phase 2) refuses to start if the live database has been migrated past this
// point by a newer server, so that the binary never runs against rows whose
// columns or constraints it cannot reason about.
//
// HACK(HUSHHQ-83, 2026-05-29): until phase 3 ships, this constant must be
// bumped manually whenever a new `NNNNNN_*.up.sql` lands in migrations/.
// A drift between this value and the highest migration file is silent
// until a self-host upgrade flow goes through the handshake. Phase 3
// introduces a CI lint that fails the build on drift; remove this HACK
// note when that lint is wired up.
const CurrentDBSchemaVersion = 37

// MinCompatibleDBSchemaVersion is the lowest schema_migrations.version this
// binary can operate against safely. Today it equals CurrentDBSchemaVersion
// because no rolling-back compat window has been declared yet.
//
// A release that ships a destructive or non-reversible migration MUST bump
// MinCompatibleDBSchemaVersion to the new schema version to mark every prior
// schema unsupported by this binary. The HUSHHQ-83 phase 3 migration metadata
// (`compat_break: true`) is what drives this bump in practice.
const MinCompatibleDBSchemaVersion = 37

// CryptoCompatRanges is the server-authoritative compatibility envelope for
// client-side cryptographic packages. Keys are package names (matching the
// shape of hush-web/compatibility.json); values are version constraints the
// server expects the client to satisfy.
//
// The server does not consume these packages itself; the field tells the
// client which `@gethush/hush-crypto` (and any future crypto dep) build is
// safe to talk to this server. The MLS ciphersuite check on the same handshake
// response (`current_mls_ciphersuite`) covers the protocol-level guarantee;
// CryptoCompatRanges is a defensive belt against subtle API drift in the WASM
// crypto bindings.
//
// Today the only entry is `@gethush/hush-crypto`, pinned to the version
// listed in hush-web/compatibility.json. Phase 4 wires the client-side
// check.
//
// The map is owned by this package and treated as read-only at runtime.
// Callers must not mutate the returned value. `internal/version/version_test.go`
// holds the drift check against `hush-web/compatibility.json` for monorepo
// dev environments; CI-only builds where hush-web is not on disk skip the
// check with a logged note instead of failing.
var CryptoCompatRanges = map[string]string{
	"@gethush/hush-crypto": "0.2.2",
}

// KeyPackageLowThreshold is the minimum number of unused MLS KeyPackages the server
// should maintain per device. When the count drops below this value, the server emits
// a key_packages.low WS event prompting the client to upload more KeyPackages.
// Value of 10 is the well-established default carried over from the Signal OPK threshold.
const KeyPackageLowThreshold = 10

// LegacyMLSCiphersuite is the OpenMLS ciphersuite identifier used by Hush before the
// post-quantum migration: MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519. This is one
// of the base ciphersuites defined in RFC 9420 (IANA codepoint 0x0001).
// Server-side MLS state created prior to the X-Wing migration is stamped with this
// value during the migration backfill. The delivery service refuses to return or
// reuse legacy-stamped rows for new groups operating under CurrentMLSCiphersuite.
const LegacyMLSCiphersuite = 1

// CurrentMLSCiphersuite is the authoritative server-side OpenMLS ciphersuite that
// new MLS state (KeyPackages, GroupInfo, Commits, Welcomes) must be created with.
// Value is the OpenMLS ciphersuite identifier MLS_256_XWING_CHACHA20POLY1305_SHA256_Ed25519
// at IANA codepoint 0x004D (decimal 77). X-Wing is a post-quantum hybrid KEM
// registered in IANA's MLS ciphersuites registry; it is NOT a base ciphersuite of
// RFC 9420 itself, which only standardized the 0x0001-0x0007 set.
//
// The delivery service uses this constant to:
//
//  1. Stamp every new MLS row it accepts.
//  2. Filter reads so legacy ciphersuite rows are never surfaced to clients running
//     the current protocol epoch.
//  3. Advertise the active ciphersuite to clients via the public handshake response,
//     so a client can refuse to upload state for an incompatible suite before the
//     server has to reject it.
//
// Bumping this constant is a protocol-epoch event and requires a coordinated
// migration: code change, database migration that re-stamps live rows, and client
// rollout. It must NEVER be silently bumped.
const CurrentMLSCiphersuite = 77
