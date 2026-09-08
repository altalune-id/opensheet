// Package apikey is the API keys bounded context.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// Label is the wire prefix of every plaintext key.
	Label = "osk"
	// PrefixLen is the length in characters of the public prefix half.
	PrefixLen = 16
	// SecretLen is the length in characters of the base64url secret half.
	SecretLen = 43
	// MaxNameLen is the longest accepted key name, in runes.
	MaxNameLen = 100

	prefixBytes = 8
	secretBytes = 32
	sep         = "_"
)

// APIKey is the aggregate root. Invariants live here.
type APIKey struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	ProjectID  uuid.UUID
	Name       string
	KeyPrefix  string
	SecretHash []byte
	Scopes     []string
	SheetIDs   []uuid.UUID
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// Mint builds a key and returns the plaintext exactly once; only KeyPrefix and SecretHash persist.
// NOTE: catalog membership of each scope is validated by Service.Create — the aggregate may not import the scope catalog.
func Mint(orgID, projectID uuid.UUID, name string, scopes []string, sheetIDs []uuid.UUID, expiresAt *time.Time, now time.Time) (*APIKey, string, error) {
	name, err := checkName(name)
	if err != nil {
		return nil, "", err
	}
	scopeSet, err := checkScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	if err := checkExpiry(expiresAt, now); err != nil {
		return nil, "", err
	}

	prefix, secret, err := generate()
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256([]byte(secret))

	k := &APIKey{
		ID:         uuid.Must(uuid.NewV7()),
		OrgID:      orgID,
		ProjectID:  projectID,
		Name:       name,
		KeyPrefix:  prefix,
		SecretHash: sum[:],
		Scopes:     scopeSet,
		SheetIDs:   dedupeIDs(sheetIDs),
		CreatedAt:  now.UTC(),
	}
	if expiresAt != nil {
		exp := expiresAt.UTC()
		k.ExpiresAt = &exp
	}
	return k, Label + sep + prefix + sep + secret, nil
}

// Parse splits a raw key into its public prefix and secret halves.
// SECURITY: every rejection returns the same opaque *UnauthorizedError, so a malformed key is indistinguishable from an unknown one.
func Parse(raw string) (prefix, secret string, err error) { //nolint:nonamedreturns // two same-typed halves read clearer named
	// NOTE: base64url includes '_', so the secret is the remainder of the string, not a third field.
	parts := strings.SplitN(raw, sep, 3)
	if len(parts) != 3 || parts[0] != Label {
		return "", "", &UnauthorizedError{}
	}
	if len(parts[1]) != PrefixLen || !isHex(parts[1]) {
		return "", "", &UnauthorizedError{}
	}
	if len(parts[2]) != SecretLen || !isBase64URL(parts[2]) {
		return "", "", &UnauthorizedError{}
	}
	return parts[1], parts[2], nil
}

// Verify reports whether secret hashes to this key's stored digest.
func (k *APIKey) Verify(secret string) bool {
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], k.SecretHash) == 1
}

// Revoke marks the key revoked at now; a second call does not move the timestamp.
func (k *APIKey) Revoke(now time.Time) {
	if k.RevokedAt != nil {
		return
	}
	t := now.UTC()
	k.RevokedAt = &t
}

// Active reports whether the key is neither revoked nor expired at now.
func (k *APIKey) Active(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now.UTC()) {
		return false
	}
	return true
}

// Allows reports whether the key grants scope on sheetID; an empty SheetIDs means every sheet in this project.
func (k *APIKey) Allows(scope string, sheetID uuid.UUID) bool {
	if !slices.Contains(k.Scopes, scope) {
		return false
	}
	if len(k.SheetIDs) == 0 {
		return true
	}
	return slices.Contains(k.SheetIDs, sheetID)
}

//nolint:gochecknoglobals // SECURITY: fixed digest an unknown prefix is compared against; immutable, and no real secret hashes to it.
var dummySecretHash = sha256.Sum256([]byte("opensheet: api key prefix miss"))

// SECURITY: pays the same hash-and-compare cost as Verify so an unknown prefix does not leak prefix validity by timing.
func dummyVerify(secret string) bool {
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], dummySecretHash[:]) == 1
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &InvalidNameError{Reason: "empty"}
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return "", &InvalidNameError{Reason: fmt.Sprintf("over %d characters", MaxNameLen)}
	}
	return name, nil
}

func checkScopes(scopes []string) ([]string, error) {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, &InvalidScopeError{Reason: "blank entry"}
		}
		if slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, &InvalidScopeError{Reason: "empty set"}
	}
	return out, nil
}

func checkExpiry(expiresAt *time.Time, now time.Time) error {
	if expiresAt == nil {
		return nil
	}
	if !expiresAt.After(now) {
		return &InvalidExpiryError{Reason: "not after now"}
	}
	return nil
}

func dedupeIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if slices.Contains(out, id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func generate() (prefix, secret string, err error) { //nolint:nonamedreturns // two same-typed halves read clearer named
	p := make([]byte, prefixBytes)
	if _, err := rand.Read(p); err != nil {
		return "", "", fmt.Errorf("apikey: mint: prefix entropy: %w", err)
	}
	s := make([]byte, secretBytes)
	if _, err := rand.Read(s); err != nil {
		return "", "", fmt.Errorf("apikey: mint: secret entropy: %w", err)
	}
	return hex.EncodeToString(p), base64.RawURLEncoding.EncodeToString(s), nil
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func isBase64URL(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
