package auth

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/password"
	"altalune.id/template/internal/platform/session"
)

// Genesis is the local-admin bootstrap identity carried as a plain value type.
type Genesis struct {
	Email        string
	PasswordHash string
	Name         string
}

// Configured reports whether both email and hash are set.
func (g Genesis) Configured() bool {
	return strings.TrimSpace(g.Email) != "" && g.PasswordHash != ""
}

// UserRef is the projection of a user.User that LocalLogin needs.
type UserRef struct {
	ID              uuid.UUID
	Email           string
	Name            string
	Source          string
	IsAdmin         bool
	Locale          string
	PasswordHash    string
	TermsAcceptedAt *time.Time
}

type userStore interface {
	ByEmail(ctx context.Context, email string) (*UserRef, error)
	Save(ctx context.Context, u *UserRef) error
}

type isNotFoundFn func(err error) bool

// LocalLogin verifies genesis credentials in constant time and returns a Principal.
type LocalLogin struct {
	users      userStore
	genesis    Genesis
	hasher     func(pw string) (string, error)
	verifier   func(hash, pw string) bool
	isNotFound isNotFoundFn
	now        func() time.Time
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// LocalOption tunes LocalLogin construction.
type LocalOption func(*LocalLogin)

// WithLocalHasher installs a hasher for future password rotation flows.
func WithLocalHasher(h func(pw string) (string, error)) LocalOption {
	return func(l *LocalLogin) { l.hasher = h }
}

// WithLocalVerifier installs a hash-vs-password verifier (e.g. bcrypt or argon2).
func WithLocalVerifier(v func(hash, pw string) bool) LocalOption {
	return func(l *LocalLogin) { l.verifier = v }
}

// WithLocalNotFound installs the "user not found" predicate for the injected store.
func WithLocalNotFound(fn isNotFoundFn) LocalOption {
	return func(l *LocalLogin) { l.isNotFound = fn }
}

// NewLocalLogin binds the workflow to its dependencies.
func NewLocalLogin(users userStore, genesis Genesis, log *slog.Logger, unexpected apperror.UnexpectedFunc, opts ...LocalOption) *LocalLogin {
	l := &LocalLogin{
		users:      users,
		genesis:    genesis,
		hasher:     defaultHasher,
		verifier:   defaultVerifier,
		isNotFound: func(_ error) bool { return false },
		now:        func() time.Time { return time.Now().UTC() },
		log:        log,
		unexpected: unexpected,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Configured reports whether local login is available via a static genesis identity.
func (l *LocalLogin) Configured() bool { return l.genesis.Configured() }

// Execute authenticates creds against a per-user password hash first, falling back to the static genesis identity.
func (l *LocalLogin) Execute(ctx context.Context, creds Credentials) (session.Principal, error) {
	ctx, span := tracer.Start(ctx, "auth.LocalLogin.Execute")
	defer span.End()

	gotEmail := strings.ToLower(strings.TrimSpace(creds.Email))
	if gotEmail == "" || creds.Password == "" {
		return session.Principal{}, &InvalidCredentialsError{}
	}

	u, err := l.users.ByEmail(ctx, gotEmail)
	switch {
	case err == nil:
		if u.PasswordHash != "" {
			if err := password.Verify(u.PasswordHash, creds.Password); err != nil {
				return session.Principal{}, &InvalidCredentialsError{}
			}
			span.SetAttributes(attribute.String("user.id", u.ID.String()))
			return principalFromRef(u, sourceFor(u), l.now()), nil
		}
	case !l.isNotFound(err):
		return session.Principal{}, l.unexpected(ctx, "auth.LocalLogin.Execute: byEmail", err)
	}

	if !l.Configured() {
		return session.Principal{}, &InvalidCredentialsError{}
	}
	wantEmail := strings.ToLower(strings.TrimSpace(l.genesis.Email))
	emailOK := subtle.ConstantTimeCompare([]byte(gotEmail), []byte(wantEmail)) == 1
	passwordOK := l.verifier(l.genesis.PasswordHash, creds.Password)
	if !emailOK || !passwordOK {
		return session.Principal{}, &InvalidCredentialsError{}
	}

	if u != nil {
		span.SetAttributes(attribute.String("user.id", u.ID.String()))
		return principalFromRef(u, session.SourceGenesis, l.now()), nil
	}

	name := strings.TrimSpace(l.genesis.Name)
	if name == "" {
		name = "Root"
	}
	genesisUser := &UserRef{
		ID:     uuid.New(),
		Email:  wantEmail,
		Name:   name,
		Source: string(session.SourceGenesis),
	}
	if err := l.users.Save(ctx, genesisUser); err != nil {
		return session.Principal{}, l.unexpected(ctx, "auth.LocalLogin.Execute: save genesis", err)
	}
	span.SetAttributes(attribute.String("user.id", genesisUser.ID.String()))
	return principalFromRef(genesisUser, session.SourceGenesis, l.now()), nil
}

func sourceFor(u *UserRef) session.Source {
	if strings.EqualFold(u.Source, string(session.SourceLocal)) {
		return session.SourceLocal
	}
	if strings.EqualFold(u.Source, string(session.SourceOIDC)) {
		return session.SourceOIDC
	}
	return session.SourceGenesis
}

func principalFromRef(u *UserRef, src session.Source, now time.Time) session.Principal {
	p := session.Principal{
		UserID:   u.ID,
		Email:    u.Email,
		Name:     u.Name,
		Source:   src,
		IsAdmin:  u.IsAdmin,
		Locale:   u.Locale,
		IssuedAt: now,
	}
	if u.TermsAcceptedAt != nil {
		p.TermsAcceptedAt = *u.TermsAcceptedAt
	}
	return p
}

func defaultHasher(pw string) (string, error) {
	return password.Hash(pw)
}

func defaultVerifier(hash, pw string) bool {
	return password.Verify(hash, pw) == nil
}
