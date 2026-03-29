package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds environment-based configuration for the Hush API.
type Config struct {
	Port         int
	DatabaseURL  string
	JWTSecret    string
	JWTExpiry    time.Duration
	CORSOrigin   string
	Production   bool
	AdminAPIKey              string // X-Admin-Key header value for /api/admin; empty disables admin routes
	LiveKitAPIKey            string
	LiveKitAPISecret         string
	LiveKitURL               string
	// TransparencyLogPrivateKey is the hex-encoded 32-byte Ed25519 seed for the
	// transparency log signing keypair. If empty in dev mode, an ephemeral key
	// is generated with a warning. Required in production.
	TransparencyLogPrivateKey string
}

// Load reads configuration from environment variables.
func Load() Config {
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	jwtExpiryHours := 168 // 7 days
	if h := os.Getenv("JWT_EXPIRY_HOURS"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 {
			jwtExpiryHours = v
		}
	}
	prod := os.Getenv("PRODUCTION")
	production := prod == "1" || prod == "true" || prod == "yes"

	corsOrigin := os.Getenv("CORS_ORIGIN")
	if corsOrigin == "" {
		if domain := os.Getenv("DOMAIN"); domain != "" {
			corsOrigin = "https://" + domain
		}
	}

	return Config{
		Port:                     port,
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		JWTSecret:                os.Getenv("JWT_SECRET"),
		JWTExpiry:                time.Duration(jwtExpiryHours) * time.Hour,
		CORSOrigin:               corsOrigin,
		Production:               production,
		AdminAPIKey:              os.Getenv("ADMIN_API_KEY"),
		LiveKitAPIKey:            os.Getenv("LIVEKIT_API_KEY"),
		LiveKitAPISecret:         os.Getenv("LIVEKIT_API_SECRET"),
		LiveKitURL:               os.Getenv("LIVEKIT_URL"),
		TransparencyLogPrivateKey: os.Getenv("TRANSPARENCY_LOG_PRIVATE_KEY"),
	}
}
