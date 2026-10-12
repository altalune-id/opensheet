package apikey_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
)

func TestMintReturnsPlaintextOnceAndStoresOnlyAHash(t *testing.T) {
	now := time.Now().UTC()
	k, plaintext, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopeSheetsRead}, nil, nil, now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !strings.HasPrefix(plaintext, apikey.DefaultPrefix) {
		t.Fatalf("plaintext %q lacks prefix %q", plaintext, apikey.DefaultPrefix)
	}
	if strings.Contains(plaintext, "\n") || len(plaintext) < 32 {
		t.Fatalf("plaintext looks malformed: %q", plaintext)
	}
	if k.SecretHash == [32]byte{} {
		t.Fatal("SecretHash is zero")
	}
}

func TestMintRejectsUnknownScope(t *testing.T) {
	_, _, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "bad", []string{"posts:destroy"}, nil, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("Mint accepted a scope outside the catalog")
	}
	if !apikey.IsUnknownScopeError(err) {
		t.Fatalf("err = %v, want *UnknownScopeError", err)
	}
}

func TestUsable(t *testing.T) {
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	k, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopeSheetsRead}, nil, nil, now)
	if !k.Usable(now) {
		t.Fatal("fresh key not usable")
	}

	expired, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopeSheetsRead}, nil, &past, now)
	if expired.Usable(now) {
		t.Fatal("expired key usable")
	}

	notYet, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopeSheetsRead}, nil, &future, now)
	if !notYet.Usable(now) {
		t.Fatal("unexpired key not usable")
	}

	revoked, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopeSheetsRead}, nil, nil, now)
	revoked.RevokedAt = &past
	if revoked.Usable(now) {
		t.Fatal("revoked key usable")
	}
}
