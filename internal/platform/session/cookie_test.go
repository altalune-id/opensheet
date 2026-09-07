package session_test

import (
	"strings"
	"testing"

	"altalune.id/template/internal/platform/session"
)

func TestSignVerify_Roundtrip(t *testing.T) {
	secret := []byte("supersecret-1234567890abcdef")
	sig := session.Sign(secret, "sid123")
	got, err := session.Verify(secret, sig)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sid123" {
		t.Errorf("got %q", got)
	}
}

func TestVerify_TamperedFails(t *testing.T) {
	secret := []byte("supersecret-1234567890abcdef")
	sig := session.Sign(secret, "sid123")
	parts := strings.SplitN(sig, "|", 2)
	tampered := "different|" + parts[1]
	if _, err := session.Verify(secret, tampered); err == nil {
		t.Error("expected verify to fail on tamper")
	}
}

func TestVerify_Malformed(t *testing.T) {
	secret := []byte("supersecret-1234567890abcdef")
	for _, raw := range []string{"", "novertbar", "|", "value|", "|sig", "value|not-base64!"} {
		if _, err := session.Verify(secret, raw); err == nil {
			t.Errorf("expected error on %q", raw)
		}
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	sig := session.Sign([]byte("secretA-1234567890abcdef1234"), "sid")
	if _, err := session.Verify([]byte("secretB-1234567890abcdef1234"), sig); err == nil {
		t.Error("expected wrong-secret to fail")
	}
}
