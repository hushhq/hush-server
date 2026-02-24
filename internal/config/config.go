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
	LiveKitAPIKey    string
	LiveKitAPISecret string
	LiveKitURL       string
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
	return Config{
		Port:             port,
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
		JWTExpiry:        time.Duration(jwtExpiryHours) * time.Hour,
		CORSOrigin:       os.Getenv("CORS_ORIGIN"),
		LiveKitAPIKey:    os.Getenv("LIVEKIT_API_KEY"),
		LiveKitAPISecret: os.Getenv("LIVEKIT_API_SECRET"),
		LiveKitURL:       os.Getenv("LIVEKIT_URL"),
	}
}
