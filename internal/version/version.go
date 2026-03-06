// Package version provides build-time injectable version variables and protocol constants.
//
// ServerVersion, APIVersion, and MinClientVersion can be overridden at build time via ldflags:
//
//	go build -ldflags "-X hush.app/server/internal/version.ServerVersion=1.0.0 \
//	  -X hush.app/server/internal/version.APIVersion=v2 \
//	  -X hush.app/server/internal/version.MinClientVersion=0.5.0"
package version

// ServerVersion is the server release version. Defaults to "dev" for local builds.
// Override via: -ldflags "-X hush.app/server/internal/version.ServerVersion=x.y.z"
var ServerVersion = "dev"

// APIVersion is the API version prefix. Defaults to "v1".
// Override via: -ldflags "-X hush.app/server/internal/version.APIVersion=v2"
var APIVersion = "v1"

// MinClientVersion is the minimum client version required to connect. Defaults to "0.0.0".
// Override via: -ldflags "-X hush.app/server/internal/version.MinClientVersion=x.y.z"
var MinClientVersion = "0.0.0"

// OPKLowThreshold is the minimum number of one-time pre-keys the server should
// maintain per device. When the count drops below this value, the server emits
// a keys.low event prompting the client to upload more OPKs. This matches the
// well-established Signal Protocol default and is not user-configurable.
const OPKLowThreshold = 10
