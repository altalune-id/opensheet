package user

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/password"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer trace.Tracer = otel.Tracer("altalune.id/opensheet/internal/user")

// GenesisConfig names the built-in admin identity reconciled by ReconcileGenesisAdmin.
type GenesisConfig struct {
	Email    string
	Password string
}

// Claims is the subset of an OIDC identity token used by EnsureFromOIDC.
type Claims struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// InviteFinder reports whether an email has any pending invite; used by CheckOIDCSignupEligibility.
type InviteFinder interface {
	HasPendingForEmail(ctx context.Context, email string) (bool, error)
}

// Service is the driving port for user use cases.
type Service struct {
	store      Store
	invites    InviteFinder
	genesis    GenesisConfig
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	now        func() time.Time
}

// ServiceOption tunes Service construction.
type ServiceOption func(*Service)

// WithInviteFinder attaches an invite lookup used by CheckOIDCSignupEligibility.
func WithInviteFinder(f InviteFinder) ServiceOption {
	return func(s *Service) { s.invites = f }
}

// NewService wires a Service with the canonical dependencies plus optional genesis identity.
func NewService(store Store, genesis GenesisConfig, log *slog.Logger, unexpected apperror.UnexpectedFunc, opts ...ServiceOption) *Service {
	s := &Service{
		store:      store,
		genesis:    genesis,
		log:        log,
		unexpected: unexpected,
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CheckOIDCSignupEligibility reports whether the OIDC signup for email is allowed in a selfhosted invite-only deployment.
func (s *Service) CheckOIDCSignupEligibility(ctx context.Context, email string) error {
	ctx, span := tracer.Start(ctx, "user.CheckOIDCSignupEligibility")
	defer span.End()
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return &InvalidEmailError{Reason: "empty"}
	}
	if _, err := s.store.ByEmail(ctx, e); err == nil {
		return nil
	} else if !IsNotFoundError(err) {
		return s.unexpected(ctx, "user.CheckOIDCSignupEligibility: byEmail", err)
	}
	if s.invites != nil {
		has, err := s.invites.HasPendingForEmail(ctx, e)
		if err != nil {
			return s.unexpected(ctx, "user.CheckOIDCSignupEligibility: invites", err)
		}
		if has {
			return nil
		}
	}
	return &NotInvitedError{Email: e}
}

// CreateRequest carries the inputs for Service.Create.
type CreateRequest struct {
	Email    string
	Name     string
	Source   string
	Password string
}

// Create constructs and persists a user; a non-empty Password is Argon2id-hashed before storage.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*User, error) {
	ctx, span := tracer.Start(ctx, "user.Create")
	defer span.End()
	u, err := New(req.Email, req.Name, req.Source)
	if err != nil {
		return nil, err
	}
	if pw := strings.TrimSpace(req.Password); pw != "" {
		hash, err := password.Hash(pw)
		if err != nil {
			return nil, fmt.Errorf("user.Create: hash: %w", err)
		}
		u.PasswordHash = hash
	}
	if err := s.store.Save(ctx, u); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "user.Create: save", err)
	}
	return u, nil
}

// Promote grants admin privileges to the identified user; idempotent.
func (s *Service) Promote(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "user.Promote")
	defer span.End()
	u, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "user.Promote: byID", err, slog.String("user_id", id.String()))
	}
	if u.IsAdmin {
		return nil
	}
	u.IsAdmin = true
	if err := s.store.Save(ctx, u); err != nil {
		return s.unexpected(ctx, "user.Promote: save", err, slog.String("user_id", id.String()))
	}
	return nil
}

// HasLocalUsers reports whether any stored user carries a non-empty password hash.
func (s *Service) HasLocalUsers(ctx context.Context) (bool, error) {
	ctx, span := tracer.Start(ctx, "user.HasLocalUsers")
	defer span.End()
	has, err := s.store.HasLocalUsers(ctx)
	if err != nil {
		return false, s.unexpected(ctx, "user.HasLocalUsers", err)
	}
	return has, nil
}

// Outcome reports what ReconcileGenesisAdmin found for the configured genesis address.
type Outcome string

// Outcomes reported by ReconcileGenesisAdmin.
const (
	OutcomeUnconfigured Outcome = "unconfigured"
	OutcomeSatisfied    Outcome = "satisfied"
	OutcomeClaimed      Outcome = "claimed"
	OutcomeUnclaimed    Outcome = "unclaimed"
)

// ReconcileGenesisAdmin promotes the configured genesis address when a matching user exists, writing nothing otherwise.
func (s *Service) ReconcileGenesisAdmin(ctx context.Context) (Outcome, error) {
	ctx, span := tracer.Start(ctx, "user.ReconcileGenesisAdmin")
	defer span.End()

	email := strings.ToLower(strings.TrimSpace(s.genesis.Email))
	if email == "" {
		return OutcomeUnconfigured, nil
	}
	u, err := s.store.ByEmail(ctx, email)
	if err != nil {
		if IsNotFoundError(err) {
			return OutcomeUnclaimed, nil
		}
		return "", s.unexpected(ctx, "user.ReconcileGenesisAdmin: byEmail", err)
	}
	if u.IsAdmin {
		return OutcomeSatisfied, nil
	}
	if err := s.Promote(ctx, u.ID); err != nil {
		return "", err
	}
	return OutcomeClaimed, nil
}

// EnsureFromOIDC upserts the user identified by claims.
func (s *Service) EnsureFromOIDC(ctx context.Context, claims Claims) (*User, error) {
	ctx, span := tracer.Start(ctx, "user.EnsureFromOIDC")
	defer span.End()
	if strings.TrimSpace(claims.Issuer) == "" || strings.TrimSpace(claims.Subject) == "" {
		return nil, &InvalidEmailError{Reason: "oidc claims missing issuer or subject"}
	}
	issuer := strings.TrimSpace(claims.Issuer)
	subject := strings.TrimSpace(claims.Subject)
	email := strings.ToLower(strings.TrimSpace(claims.Email))

	// SECURITY: the subject is the stable identity, not the email; resolving by email first breaks login when the IdP address changes.
	linked, err := s.store.ByIDP(ctx, issuer, subject)
	if err == nil {
		return s.refreshFromClaims(ctx, linked, claims)
	}
	if !IsNotFoundError(err) {
		return nil, s.unexpected(ctx, "user.EnsureFromOIDC: byIDP", err)
	}

	// SECURITY: an unknown subject is matched or bound by its email, so the issuer must vouch for that email.
	if !claims.EmailVerified {
		return nil, &EmailUnverifiedError{}
	}
	existing, err := s.store.ByEmail(ctx, email)
	if err == nil {
		return s.relink(ctx, existing, issuer, subject, claims)
	}
	if !IsNotFoundError(err) {
		return nil, s.unexpected(ctx, "user.EnsureFromOIDC: byEmail", err)
	}
	name := cmp.Or(strings.TrimSpace(claims.Name), defaultNameFromEmail(email))
	u, err := New(claims.Email, name, SourceOIDC)
	if err != nil {
		return nil, err
	}
	u.IDPIssuer = issuer
	u.IDPSubject = subject
	if err := s.store.Save(ctx, u); err != nil {
		var dup *AlreadyExistsError
		if errors.As(err, &dup) {
			if dup.Field == "idp_subject" {
				return s.store.ByIDP(ctx, issuer, subject)
			}
			return s.store.ByEmail(ctx, u.Email)
		}
		return nil, s.unexpected(ctx, "user.EnsureFromOIDC: save", err)
	}
	return u, nil
}

// SECURITY: a verified email rebinds an unlinked row or one linked at another issuer; another subject at the same issuer is a different person.
func (s *Service) relink(ctx context.Context, u *User, issuer, subject string, claims Claims) (*User, error) {
	if u.IDPSubject != "" && u.IDPIssuer == issuer && u.IDPSubject != subject {
		return nil, &IdentityConflictError{}
	}
	if name := strings.TrimSpace(claims.Name); name != "" && u.Name != name {
		if err := u.Rename(name); err != nil {
			return nil, err
		}
	}
	u.IDPIssuer = issuer
	u.IDPSubject = subject
	if err := s.store.Save(ctx, u); err != nil {
		return nil, s.unexpected(ctx, "user.EnsureFromOIDC: relink", err)
	}
	return u, nil
}

func (s *Service) refreshFromClaims(ctx context.Context, u *User, claims Claims) (*User, error) {
	dirty := false
	if name := strings.TrimSpace(claims.Name); name != "" && u.Name != name {
		if err := u.Rename(name); err != nil {
			return nil, err
		}
		dirty = true
	}
	if email := strings.ToLower(strings.TrimSpace(claims.Email)); claims.EmailVerified && email != "" && u.Email != email {
		if err := u.ChangeEmail(email); err != nil {
			return nil, err
		}
		dirty = true
	}
	if !dirty {
		return u, nil
	}
	if err := s.store.Save(ctx, u); err != nil {
		if dup, ok := errors.AsType[*AlreadyExistsError](err); ok {
			if dup.Field == "email" {
				return nil, &IdentityConflictError{Cause: err}
			}
			return nil, err
		}
		return nil, s.unexpected(ctx, "user.EnsureFromOIDC: refresh", err)
	}
	return u, nil
}

// AcceptTerms records the moment the user accepted the ToS; idempotent.
func (s *Service) AcceptTerms(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "user.AcceptTerms")
	defer span.End()
	u, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "user.AcceptTerms: byID", err, slog.String("user_id", id.String()))
	}
	if u.TermsAcceptedAt != nil {
		return nil
	}
	u.AcceptTerms(s.now())
	if err := s.store.Save(ctx, u); err != nil {
		return s.unexpected(ctx, "user.AcceptTerms: save", err, slog.String("user_id", id.String()))
	}
	return nil
}

// UpdateLocale persists the user's preferred locale tag.
func (s *Service) UpdateLocale(ctx context.Context, id uuid.UUID, locale string) error {
	ctx, span := tracer.Start(ctx, "user.UpdateLocale")
	defer span.End()
	locale = strings.TrimSpace(locale)
	if err := s.store.UpdateLocale(ctx, id, locale); err != nil {
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "user.UpdateLocale", err, slog.String("user_id", id.String()))
	}
	return nil
}

// ByID looks up a user by aggregate id.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*User, error) {
	ctx, span := tracer.Start(ctx, "user.ByID")
	defer span.End()
	u, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "user.ByID: byID", err, slog.String("user_id", id.String()))
	}
	return u, nil
}

// Rename updates the user's display name.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	ctx, span := tracer.Start(ctx, "user.Rename")
	defer span.End()
	u, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "user.Rename: byID", err, slog.String("user_id", id.String()))
	}
	if err := u.Rename(name); err != nil {
		return err
	}
	if err := s.store.Save(ctx, u); err != nil {
		return s.unexpected(ctx, "user.Rename: save", err, slog.String("user_id", id.String()))
	}
	return nil
}
