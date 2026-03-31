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
var MinClientVersion = "0.0.0"

// KeyPackageLowThreshold is the minimum number of unused MLS KeyPackages the server
// should maintain per device. When the count drops below this value, the server emits
// a key_packages.low WS event prompting the client to upload more KeyPackages.
// Value of 10 is the well-established default carried over from the Signal OPK threshold.
const KeyPackageLowThreshold = 10
