package web_test

import (
	"strings"
	"testing"

	"altalune.id/template/internal/web"
)

func TestSignVerifyCookie_Roundtrip(t *testing.T) {
	secret := []byte("supersecret-1234567890abcdef")
	sig := web.SignCookie(secret, "sid123")
	got, err := web.VerifyCookie(secret, sig)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sid123" {
		t.Errorf("got %q", got)
	}
}

func TestVerifyCookie_TamperedFails(t *testing.T) {
	secret := []byte("supersecret-1234567890abcdef")
	sig := web.SignCookie(secret, "sid123")
	// Flip a bit in the value part
	parts := strings.SplitN(sig, "|", 2)
	tampered := "different|" + parts[1]
	if _, err := web.VerifyCookie(secret, tampered); err == nil {
		t.Error("expected verify to fail on tamper")
	}
}

func TestVerifyCookie_Malformed(t *testing.T) {
	secret := []byte("supersecret-1234567890abcdef")
	for _, raw := range []string{"", "novertbar", "|", "value|", "|sig", "value|not-base64!"} {
		if _, err := web.VerifyCookie(secret, raw); err == nil {
			t.Errorf("expected error on %q", raw)
		}
	}
}

func TestVerifyCookie_WrongSecret(t *testing.T) {
	sig := web.SignCookie([]byte("secretA-1234567890abcdef1234"), "sid")
	if _, err := web.VerifyCookie([]byte("secretB-1234567890abcdef1234"), sig); err == nil {
		t.Error("expected wrong-secret to fail")
	}
}

func TestNewSID_Unique(t *testing.T) {
	a, err := web.NewSID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := web.NewSID()
	if a == b || a == "" {
		t.Errorf("expected distinct nonempty SIDs, got %q,%q", a, b)
	}
}
