package nanoid

import (
	"strings"
	"testing"
)

func TestNew_Length(t *testing.T) {
	for _, n := range []int{8, 16, 21, 32} {
		s, err := New(n)
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != n {
			t.Errorf("len(%q)=%d, want %d", s, len(s), n)
		}
	}
}

func TestNew_Alphabet(t *testing.T) {
	s, _ := New(64)
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for _, r := range s {
		if !strings.ContainsRune(allowed, r) {
			t.Fatalf("unexpected character %q in %q", r, s)
		}
	}
}

func TestNew_ZeroLength(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Fatal("expected error for length 0")
	}
}

func TestNewInviteToken_Deterministic(t *testing.T) {
	raw, hash, err := NewInviteToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 21 {
		t.Errorf("raw len=%d, want >=21", len(raw))
	}
	if len(hash) != 64 {
		t.Errorf("hash len=%d, want 64", len(hash))
	}
}

func TestNewInviteToken_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		raw, _, _ := NewInviteToken()
		if seen[raw] {
			t.Fatalf("collision at iter %d: %q", i, raw)
		}
		seen[raw] = true
	}
}
