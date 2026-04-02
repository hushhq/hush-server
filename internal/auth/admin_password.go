package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	adminPasswordMemoryKiB   = 64 * 1024
	adminPasswordIterations  = 3
	adminPasswordParallelism = 1
	adminPasswordSaltSize    = 16
	adminPasswordKeySize     = 32
)

// HashAdminPassword derives an Argon2id password hash suitable for storage.
func HashAdminPassword(password string) (string, error) {
	salt := make([]byte, adminPasswordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		adminPasswordIterations,
		adminPasswordMemoryKiB,
		adminPasswordParallelism,
		adminPasswordKeySize,
	)
	return fmt.Sprintf(
		"argon2id$%d$%d$%d$%s$%s",
		adminPasswordIterations,
		adminPasswordMemoryKiB,
		adminPasswordParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyAdminPassword compares a stored Argon2id hash against the supplied password.
func VerifyAdminPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false, fmt.Errorf("invalid password hash format")
	}

	var iterations uint32
	var memoryKiB uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil {
		return false, fmt.Errorf("parse password hash iterations: %w", err)
	}
	if _, err := fmt.Sscanf(parts[2], "%d", &memoryKiB); err != nil {
		return false, fmt.Errorf("parse password hash memory: %w", err)
	}
	if _, err := fmt.Sscanf(parts[3], "%d", &parallelism); err != nil {
		return false, fmt.Errorf("parse password hash parallelism: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode password hash salt: %w", err)
	}
	expectedKey, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode password hash key: %w", err)
	}
	actualKey := argon2.IDKey(
		[]byte(password),
		salt,
		iterations,
		memoryKiB,
		parallelism,
		uint32(len(expectedKey)),
	)
	return subtle.ConstantTimeCompare(actualKey, expectedKey) == 1, nil
}
