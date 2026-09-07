package auth_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/password"
	"altalune.id/template/internal/platform/session"
)

type fakeUserStore struct {
	mu      sync.Mutex
	byEmail map[string]*auth.UserRef
	saveErr error
	byErr   error
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{byEmail: map[string]*auth.UserRef{}}
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }

func (f *fakeUserStore) ByEmail(_ context.Context, email string) (*auth.UserRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byErr != nil {
		return nil, f.byErr
	}
	u, ok := f.byEmail[email]
	if !ok {
		return nil, notFoundErr{}
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUserStore) Save(_ context.Context, u *auth.UserRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	cp := *u
	f.byEmail[u.Email] = &cp
	return nil
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func noopUnexpected() apperror.UnexpectedFunc {
	return func(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
		return apperror.New(apperror.CodeUnexpectedError, message, 0).WithCause(cause)
	}
}

func exactVerifier(hash, pw string) bool { return hash == pw }

func TestLocalLogin_NotConfigured(t *testing.T) {
	t.Parallel()
	l := auth.NewLocalLogin(newFakeUserStore(), auth.Genesis{}, newTestLogger(), noopUnexpected(),
		auth.WithLocalVerifier(exactVerifier),
		auth.WithLocalNotFound(func(err error) bool { return errors.As(err, new(notFoundErr)) }),
	)
	if l.Configured() {
		t.Fatal("expected Configured=false")
	}
	_, err := l.Execute(context.Background(), auth.Credentials{Email: "a@b.co", Password: "p"})
	if !auth.IsInvalidCredentialsError(err) {
		t.Errorf("want InvalidCredentialsError, got %T: %v", err, err)
	}
}

func TestLocalLogin_PerUserArgon(t *testing.T) {
	t.Parallel()
	hash, err := password.Hash("s3cret-passphrase")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := newFakeUserStore()
	seed := &auth.UserRef{
		ID:           uuid.New(),
		Email:        "alice@example.com",
		Name:         "Alice",
		Source:       "local",
		PasswordHash: hash,
	}
	_ = store.Save(context.Background(), seed)

	l := auth.NewLocalLogin(store, auth.Genesis{}, newTestLogger(), noopUnexpected(),
		auth.WithLocalNotFound(func(err error) bool { return errors.As(err, new(notFoundErr)) }),
	)
	p, err := l.Execute(context.Background(), auth.Credentials{Email: "Alice@Example.com", Password: "s3cret-passphrase"})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if p.UserID != seed.ID {
		t.Errorf("userID=%v want %v", p.UserID, seed.ID)
	}
	if p.Source != session.SourceLocal {
		t.Errorf("source=%q want %q", p.Source, session.SourceLocal)
	}
	if _, err := l.Execute(context.Background(), auth.Credentials{Email: "alice@example.com", Password: "wrong"}); !auth.IsInvalidCredentialsError(err) {
		t.Errorf("expected InvalidCredentialsError, got %v", err)
	}
}

func TestLocalLogin_TableDriven(t *testing.T) {
	t.Parallel()
	genesis := auth.Genesis{Email: "admin@example.com", PasswordHash: "root", Name: "Root"}

	cases := []struct {
		name           string
		seed           *auth.UserRef
		creds          auth.Credentials
		wantErrIs      func(error) bool
		wantSameUserID bool
		wantSaved      bool
	}{
		{
			name:      "wrong email",
			creds:     auth.Credentials{Email: "someone@else.com", Password: "root"},
			wantErrIs: auth.IsInvalidCredentialsError,
		},
		{
			name:      "wrong password",
			creds:     auth.Credentials{Email: "admin@example.com", Password: "nope"},
			wantErrIs: auth.IsInvalidCredentialsError,
		},
		{
			name:           "ok existing user",
			seed:           &auth.UserRef{ID: uuid.New(), Email: "admin@example.com", Name: "Root", Source: "genesis"},
			creds:          auth.Credentials{Email: "  Admin@Example.com ", Password: "root"},
			wantSameUserID: true,
		},
		{
			name:      "ok first login creates user",
			creds:     auth.Credentials{Email: "admin@example.com", Password: "root"},
			wantSaved: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newFakeUserStore()
			if tc.seed != nil {
				_ = store.Save(context.Background(), tc.seed)
			}
			l := auth.NewLocalLogin(store, genesis, newTestLogger(), noopUnexpected(),
				auth.WithLocalVerifier(exactVerifier),
				auth.WithLocalNotFound(func(err error) bool { return errors.Is(err, notFoundErr{}) || errors.As(err, new(notFoundErr)) }),
			)
			p, err := l.Execute(context.Background(), tc.creds)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("expected error")
				}
				if !tc.wantErrIs(err) {
					t.Fatalf("got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if p.Source != session.SourceGenesis {
				t.Errorf("source=%q want genesis", p.Source)
			}
			if p.Email != "admin@example.com" {
				t.Errorf("email=%q", p.Email)
			}
			if tc.wantSameUserID && tc.seed != nil && p.UserID != tc.seed.ID {
				t.Errorf("userID=%v want %v", p.UserID, tc.seed.ID)
			}
			if tc.wantSaved {
				if _, err := store.ByEmail(context.Background(), "admin@example.com"); err != nil {
					t.Errorf("expected user saved: %v", err)
				}
			}
		})
	}
}

func TestLocalLogin_UnexpectedByEmailErrorRoutes(t *testing.T) {
	t.Parallel()
	store := newFakeUserStore()
	store.byErr = errors.New("driver down")
	genesis := auth.Genesis{Email: "admin@example.com", PasswordHash: "root"}

	var routed bool
	l := auth.NewLocalLogin(store, genesis, newTestLogger(),
		func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
			routed = true
			return apperror.New(apperror.CodeUnexpectedError, "unexpected", 0).WithCause(cause)
		},
		auth.WithLocalVerifier(exactVerifier),
		auth.WithLocalNotFound(func(_ error) bool { return false }),
	)
	_, err := l.Execute(context.Background(), auth.Credentials{Email: "admin@example.com", Password: "root"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !routed {
		t.Error("unexpected func not invoked")
	}
}
