package session

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/sealer"
)

func testSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	s, err := sealer.New(make([]byte, sealer.KeyLen))
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

func TestPrincipalCodec_RoundTrip(t *testing.T) {
	t.Parallel()
	sl := testSealer(t)
	want := Principal{
		UserID:   uuid.Must(uuid.NewV7()),
		Email:    "admin@local",
		Source:   SourceOIDC,
		IDToken:  "header.payload.signature",
		Scopes:   []string{"sheets:read"},
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	}

	blob, err := sealPrincipal(sl, "sid-1", want)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(string(blob), want.IDToken) {
		t.Fatal("the ID token must not be readable in the stored bytes")
	}

	got, err := openPrincipal(sl, "sid-1", blob)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got.UserID != want.UserID {
		t.Errorf("UserID = %v, want %v", got.UserID, want.UserID)
	}
	if got.Email != want.Email {
		t.Errorf("Email = %q, want %q", got.Email, want.Email)
	}
	if got.Source != want.Source {
		t.Errorf("Source = %q, want %q", got.Source, want.Source)
	}
	if got.IDToken != want.IDToken {
		t.Errorf("IDToken did not survive the round trip")
	}
	if !slices.Equal(got.Scopes, want.Scopes) {
		t.Errorf("Scopes = %v, want %v", got.Scopes, want.Scopes)
	}
	if !got.IssuedAt.Equal(want.IssuedAt) {
		t.Errorf("IssuedAt = %v, want %v", got.IssuedAt, want.IssuedAt)
	}
}

func TestPrincipalCodec_RefusesAnotherSid(t *testing.T) {
	t.Parallel()
	sl := testSealer(t)
	blob, err := sealPrincipal(sl, "sid-1", Principal{Email: "a@b"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err = openPrincipal(sl, "sid-2", blob); err == nil {
		t.Fatal("the sid is the AAD, so a row lifted to another sid must not open")
	}
}
