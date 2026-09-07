package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/session"
)

// EnsureClaims mirrors user.Claims without hard-importing the user module.
type EnsureClaims struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
}

// OnboardRequest mirrors user.OnboardWorkflow's inputs.
type OnboardRequest struct {
	UserID uuid.UUID
	Email  string
}

// OnboardResult carries the active org + project ids that OIDC login should activate.
type OnboardResult struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
}

// EnsureFromOIDCFn upserts a user for the given OIDC claims; the bool reports whether the user was newly created.
type EnsureFromOIDCFn func(ctx context.Context, claims EnsureClaims) (*UserRef, bool, error)

// OnboardFn resolves the active (org, project) for the authenticated user.
type OnboardFn func(ctx context.Context, req OnboardRequest) (OnboardResult, error)

// AllowSignupFn guards the OIDC signup path before any user row is persisted.
type AllowSignupFn func(ctx context.Context, email string) error

// IsSignupRequiredFn identifies errors that mean "let the login succeed with an empty active tenant".
type IsSignupRequiredFn func(err error) bool

// OIDCLogin composes the ensure-user + onboard steps and produces a Principal.
type OIDCLogin struct {
	ensureFromOIDC   EnsureFromOIDCFn
	onboard          OnboardFn
	allowSignup      AllowSignupFn
	isSignupRequired IsSignupRequiredFn
	now              func() time.Time
	log              *slog.Logger
	unexpected       apperror.UnexpectedFunc
}

// OIDCOption tunes OIDCLogin construction.
type OIDCOption func(*OIDCLogin)

// WithAllowSignup installs a pre-persistence eligibility check called before ensureFromOIDC.
func WithAllowSignup(fn AllowSignupFn) OIDCOption {
	return func(o *OIDCLogin) { o.allowSignup = fn }
}

// WithSignupRequired installs the predicate that lets onboard errors pass as successful logins with an empty active tenant.
func WithSignupRequired(fn IsSignupRequiredFn) OIDCOption {
	return func(o *OIDCLogin) { o.isSignupRequired = fn }
}

// NewOIDCLogin binds the workflow to its collaborators.
func NewOIDCLogin(ensureFromOIDC EnsureFromOIDCFn, onboard OnboardFn, log *slog.Logger, unexpected apperror.UnexpectedFunc, opts ...OIDCOption) *OIDCLogin {
	o := &OIDCLogin{
		ensureFromOIDC: ensureFromOIDC,
		onboard:        onboard,
		now:            func() time.Time { return time.Now().UTC() },
		log:            log,
		unexpected:     unexpected,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Execute validates claims, upserts the user, onboards them, and returns a Principal.
func (o *OIDCLogin) Execute(ctx context.Context, claims OIDCClaims) (session.Principal, error) {
	ctx, span := tracer.Start(ctx, "auth.OIDCLogin.Execute")
	defer span.End()

	if o.ensureFromOIDC == nil {
		return session.Principal{}, &OIDCUnavailableError{}
	}
	if strings.TrimSpace(claims.Issuer) == "" {
		return session.Principal{}, &OIDCClaimMissingError{Claim: "iss"}
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return session.Principal{}, &OIDCClaimMissingError{Claim: "sub"}
	}
	if strings.TrimSpace(claims.Email) == "" {
		return session.Principal{}, &OIDCClaimMissingError{Claim: "email"}
	}

	if o.allowSignup != nil {
		if err := o.allowSignup(ctx, claims.Email); err != nil {
			if apperr, ok := apperror.AsAppError(err); ok {
				return session.Principal{}, apperr
			}
			return session.Principal{}, o.unexpected(ctx, "auth.OIDCLogin.Execute: allowSignup", err)
		}
	}

	u, _, err := o.ensureFromOIDC(ctx, EnsureClaims(claims))
	if err != nil {
		if apperr, ok := apperror.AsAppError(err); ok {
			return session.Principal{}, apperr
		}
		return session.Principal{}, o.unexpected(ctx, "auth.OIDCLogin.Execute: ensureFromOIDC", err)
	}
	span.SetAttributes(attribute.String("user.id", u.ID.String()))

	var (
		orgID     uuid.UUID
		projectID uuid.UUID
	)
	if o.onboard != nil {
		res, err := o.onboard(ctx, OnboardRequest{UserID: u.ID, Email: u.Email})
		switch {
		case err == nil:
			orgID, projectID = res.OrgID, res.ProjectID
		case o.isSignupRequired != nil && o.isSignupRequired(err):
			// NOTE: login succeeds with a zero active tenant; the caller routes to /signup/complete.
		default:
			if apperr, ok := apperror.AsAppError(err); ok {
				return session.Principal{}, apperr
			}
			return session.Principal{}, o.unexpected(ctx, "auth.OIDCLogin.Execute: onboard", err)
		}
	}

	p := session.Principal{
		UserID:          u.ID,
		Email:           u.Email,
		Name:            u.Name,
		Source:          session.SourceOIDC,
		IDPIssuer:       claims.Issuer,
		IDPSubject:      claims.Subject,
		ActiveOrgID:     orgID,
		ActiveProjectID: projectID,
		IsAdmin:         u.IsAdmin,
		Locale:          u.Locale,
		IssuedAt:        o.now(),
	}
	if u.TermsAcceptedAt != nil {
		p.TermsAcceptedAt = *u.TermsAcceptedAt
	}
	return p, nil
}
