package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/platform/session"
)

func TestService_LocalConfigured(t *testing.T) {
	t.Parallel()
	local := auth.NewLocalLogin(newFakeUserStore(), auth.Genesis{Email: "a@b.co", PasswordHash: "h"},
		newTestLogger(), noopUnexpected(), auth.WithLocalVerifier(exactVerifier))
	s := auth.NewService(local, nil, newTestLogger(), noopUnexpected())
	if !s.LocalConfigured() {
		t.Fatal("expected LocalConfigured=true")
	}
}

func TestService_LocalConfigured_NoLocal(t *testing.T) {
	t.Parallel()
	s := auth.NewService(nil, nil, newTestLogger(), noopUnexpected())
	if s.LocalConfigured() {
		t.Fatal("expected LocalConfigured=false")
	}
}

func TestService_LoginLocal_Delegates(t *testing.T) {
	t.Parallel()
	genesis := auth.Genesis{Email: "admin@example.com", PasswordHash: "root", Name: "Root"}
	local := auth.NewLocalLogin(newFakeUserStore(), genesis, newTestLogger(), noopUnexpected(),
		auth.WithLocalVerifier(exactVerifier),
		auth.WithLocalNotFound(func(err error) bool { return errors.As(err, new(notFoundErr)) }),
	)
	s := auth.NewService(local, nil, newTestLogger(), noopUnexpected())
	p, err := s.LoginLocal(context.Background(), auth.Credentials{Email: "admin@example.com", Password: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != session.SourceGenesis {
		t.Errorf("source=%q", p.Source)
	}
}

func TestService_LoginLocal_NoLocalConfigured(t *testing.T) {
	t.Parallel()
	s := auth.NewService(nil, nil, newTestLogger(), noopUnexpected())
	_, err := s.LoginLocal(context.Background(), auth.Credentials{Email: "a@b.co", Password: "p"})
	if !auth.IsInvalidCredentialsError(err) {
		t.Errorf("want InvalidCredentialsError, got %T: %v", err, err)
	}
}

func TestService_LoginOIDC_Unavailable(t *testing.T) {
	t.Parallel()
	s := auth.NewService(nil, nil, newTestLogger(), noopUnexpected())
	_, err := s.LoginOIDC(context.Background(), auth.OIDCClaims{Issuer: "i", Subject: "s", Email: "a@b.co"})
	if !auth.IsOIDCUnavailableError(err) {
		t.Errorf("want OIDCUnavailableError, got %T: %v", err, err)
	}
}

func TestService_LoginOIDC_Delegates(t *testing.T) {
	t.Parallel()
	u := &auth.UserRef{ID: uuid.New(), Email: "alice@example.com", Name: "Alice", Source: "oidc"}
	oidc := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return u, false, nil
		},
		nil,
		newTestLogger(),
		noopUnexpected(),
	)
	s := auth.NewService(nil, oidc, newTestLogger(), noopUnexpected())
	p, err := s.LoginOIDC(context.Background(), auth.OIDCClaims{
		Issuer: "https://idp", Subject: "sub", Email: "alice@example.com", Name: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != u.ID {
		t.Errorf("userID mismatch")
	}
}
