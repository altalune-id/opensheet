package auth

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

// MembershipsFn lists the ids of every org the user belongs to.
type MembershipsFn func(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)

// TokenLogin resolves a verified bearer token to a local user and an active org.
type TokenLogin struct {
	verifier       tokens.Verifier
	ensureFromOIDC EnsureFromOIDCFn
	memberships    MembershipsFn
	log            *slog.Logger
	unexpected     apperror.UnexpectedFunc
}

// NewTokenLogin binds the workflow to its collaborators.
func NewTokenLogin(verifier tokens.Verifier, ensureFromOIDC EnsureFromOIDCFn, memberships MembershipsFn, log *slog.Logger, unexpected apperror.UnexpectedFunc) *TokenLogin {
	return &TokenLogin{
		verifier:       verifier,
		ensureFromOIDC: ensureFromOIDC,
		memberships:    memberships,
		log:            log,
		unexpected:     unexpected,
	}
}

// Authenticate verifies raw as a JWT, resolves the local user behind it, and returns a Principal.
func (t *TokenLogin) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	ctx, span := tracer.Start(ctx, "auth.TokenLogin.Authenticate")
	defer span.End()

	// SECURITY: shape first, so a foreign credential never reaches the JWKS endpoint.
	if authn.Looks(raw) != authn.ShapeJWT {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if t.verifier == nil || t.ensureFromOIDC == nil {
		return session.Principal{}, &authn.UnauthorizedError{}
	}

	verified, err := t.verifier.Verify(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}

	claims := EnsureClaims{
		Issuer:  verified.IDPIssuer,
		Subject: verified.IDPSubject,
		Email:   verified.Email,
		Name:    verified.Name,
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

	u, _, err := t.ensureFromOIDC(ctx, claims)
	if err != nil {
		if apperr, ok := apperror.AsAppError(err); ok {
			return session.Principal{}, apperr
		}
		return session.Principal{}, t.unexpected(ctx, "auth.TokenLogin.Authenticate: ensureFromOIDC", err)
	}
	span.SetAttributes(attribute.String("user.id", u.ID.String()))

	orgID, err := t.resolveOrg(ctx, verified.IDPOrgID, u.ID)
	if err != nil {
		return session.Principal{}, err
	}
	span.SetAttributes(attribute.String("org.id", orgID.String()))

	p := session.Principal{
		UserID:     u.ID,
		Email:      u.Email,
		Name:       u.Name,
		Source:     session.SourceToken,
		IDPIssuer:  verified.IDPIssuer,
		IDPSubject: verified.IDPSubject,
		IDPOrgID:   verified.IDPOrgID,
		// NOTE: spec 5.5 — a session-authenticated member is not scope-limited; the token's scope claim is not opensheet's authorization model.
		Scopes:      authn.AllScopes(),
		ActiveOrgID: orgID,
		IsAdmin:     u.IsAdmin,
		Locale:      u.Locale,
		IssuedAt:    verified.IssuedAt,
	}
	if u.TermsAcceptedAt != nil {
		p.TermsAcceptedAt = *u.TermsAcceptedAt
	}
	return p, nil
}

// SECURITY: org selection is explicit — an asserted claim or a sole membership, never the first of several.
func (t *TokenLogin) resolveOrg(ctx context.Context, idpOrgID string, userID uuid.UUID) (uuid.UUID, error) {
	if strings.TrimSpace(idpOrgID) != "" {
		orgID, err := uuid.Parse(idpOrgID)
		if err != nil {
			return uuid.Nil, &OrgUnresolvedError{Reason: "org_id claim is not a uuid"}
		}
		return orgID, nil
	}
	if t.memberships == nil {
		return uuid.Nil, &OrgUnresolvedError{Reason: "no memberships"}
	}
	orgs, err := t.memberships(ctx, userID)
	if err != nil {
		return uuid.Nil, t.unexpected(ctx, "auth.TokenLogin.Authenticate: memberships", err, slog.String("user_id", userID.String()))
	}
	if len(orgs) == 1 {
		return orgs[0], nil
	}
	if len(orgs) == 0 {
		return uuid.Nil, &OrgUnresolvedError{Reason: "no memberships"}
	}
	return uuid.Nil, &OrgUnresolvedError{Reason: "multiple memberships"}
}
