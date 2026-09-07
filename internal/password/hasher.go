// Package password hashes and verifies passwords using Argon2id in PHC string format.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// OWASP 2026 baseline for Argon2id. Params encoded into every hash so they can be tuned later without a migration.
const (
	defaultMemoryKiB uint32 = 19456
	defaultTime      uint32 = 2
	defaultThreads   uint8  = 1
	saltLen                 = 16
	keyLen           uint32 = 32
)

// ErrInvalidHash signals a malformed PHC hash string.
var ErrInvalidHash = errors.New("password: invalid hash format")

// ErrMismatch signals the password does not match the hash.
var ErrMismatch = errors.New("password: mismatch")

// Hash returns an Argon2id PHC string for the plaintext password.
func Hash(plain string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, defaultTime, defaultMemoryKiB, defaultThreads, keyLen)
	return encode(defaultMemoryKiB, defaultTime, defaultThreads, salt, key), nil
}

// Verify reports whether plain matches hash. Returns ErrMismatch on wrong password, ErrInvalidHash on malformed input.
func Verify(hash, plain string) error {
	mem, t, threads, salt, key, err := decode(hash)
	if err != nil {
		return err
	}
	other := argon2.IDKey([]byte(plain), salt, t, mem, threads, uint32(len(key))) //nolint:gosec // G115: len(key) is bounded by base64-decoded input, always safe as uint32
	if subtle.ConstantTimeCompare(key, other) != 1 {
		return ErrMismatch
	}
	return nil
}

func encode(mem, t uint32, threads uint8, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, mem, t, threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func decode(hash string) (mem, t uint32, threads uint8, salt, key []byte, err error) { //nolint:nonamedreturns,gocritic // multiple heterogeneous returns are the cleanest shape for this decoder
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &threads); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(parts[4]); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if key, err = b64.DecodeString(parts[5]); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	return mem, t, threads, salt, key, nil
}
