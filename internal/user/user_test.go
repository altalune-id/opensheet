package user_test

import (
	"testing"
	"time"

	"altalune.id/template/internal/user"
)

func TestNew_OK(t *testing.T) {
	t.Parallel()
	u, err := user.New("  Alice@Example.com  ", "  Alice ", user.SourceOIDC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email not normalized: %q", u.Email)
	}
	if u.Name != "Alice" {
		t.Errorf("name not trimmed: %q", u.Name)
	}
	if u.Source != user.SourceOIDC {
		t.Errorf("source=%q", u.Source)
	}
	if u.ID.String() == "" {
		t.Error("id must be assigned")
	}
	if u.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}
	if u.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt must be UTC, got %v", u.CreatedAt.Location())
	}
	if u.TermsAcceptedAt != nil {
		t.Error("TermsAcceptedAt must start nil")
	}
}

func TestNew_DefaultsSourceToGenesis(t *testing.T) {
	t.Parallel()
	u, err := user.New("a@b.co", "n", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if u.Source != user.SourceGenesis {
		t.Errorf("empty source should default to %q, got %q", user.SourceGenesis, u.Source)
	}
}

func TestNew_InvalidEmail(t *testing.T) {
	t.Parallel()
	cases := []string{"", "no-at-sign", "  ", "@x.com", "a@"}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := user.New(in, "name", user.SourceGenesis)
			if err == nil {
				t.Fatalf("email %q should fail", in)
			}
			if !user.IsInvalidEmailError(err) {
				t.Errorf("want IsInvalidEmailError, got %T: %v", err, err)
			}
		})
	}
}

func TestNew_InvalidName(t *testing.T) {
	t.Parallel()
	cases := []string{"", "   "}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := user.New("a@b.co", in, user.SourceGenesis)
			if err == nil {
				t.Fatalf("name %q should fail", in)
			}
			if !user.IsInvalidNameError(err) {
				t.Errorf("want IsInvalidNameError, got %T: %v", err, err)
			}
		})
	}
}

func TestRename_TrimsAndRejectsEmpty(t *testing.T) {
	t.Parallel()
	u, err := user.New("a@b.co", "old", user.SourceGenesis)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Rename("  new "); err != nil {
		t.Fatal(err)
	}
	if u.Name != "new" {
		t.Errorf("Rename didn't trim: %q", u.Name)
	}
	if err := u.Rename(""); err == nil {
		t.Error("empty rename must fail")
	} else if !user.IsInvalidNameError(err) {
		t.Errorf("want IsInvalidNameError, got %T", err)
	}
	if err := u.Rename("   "); err == nil {
		t.Error("whitespace rename must fail")
	}
}

func TestAcceptTerms(t *testing.T) {
	t.Parallel()
	u, err := user.New("a@b.co", "n", user.SourceGenesis)
	if err != nil {
		t.Fatal(err)
	}
	if u.TermsAcceptedAt != nil {
		t.Fatal("TermsAcceptedAt must start nil")
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	u.AcceptTerms(now)
	if u.TermsAcceptedAt == nil {
		t.Fatal("TermsAcceptedAt must be set")
	}
	if !u.TermsAcceptedAt.Equal(now) {
		t.Errorf("TermsAcceptedAt=%v want %v", u.TermsAcceptedAt, now)
	}
	if u.TermsAcceptedAt.Location() != time.UTC {
		t.Errorf("must be UTC")
	}
}
