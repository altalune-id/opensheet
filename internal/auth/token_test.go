package auth_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

type verifyFn func(ctx context.Context, raw string) (session.Principal, error)

func (f verifyFn) Verify(ctx context.Context, raw string) (session.Principal, error) {
	return f(ctx, raw)
}

const testJWT = "header.payload.signature"

func verifiedPrincipal(orgID string) session.Principal {
	return session.Principal{
		Email:      "member@example.com",
		Name:       "Member",
		Source:     session.SourceToken,
		IDPIssuer:  "https://idp.example.com",
		IDPSubject: "sub-1",
		IDPOrgID:   orgID,
		Scopes:     []string{"openid", "profile"},
		IssuedAt:   time.Date(2026, time.September, 8, 10, 0, 0, 0, time.UTC),
	}
}

func ensureUser(id uuid.UUID) auth.EnsureFromOIDCFn {
	return func(_ context.Context, claims auth.EnsureClaims) (*auth.UserRef, bool, error) {
		return &auth.UserRef{ID: id, Email: claims.Email, Name: claims.Name, Locale: "en"}, false, nil
	}
}

func TestTokenLogin_ForeignShapeNeverReachesVerifier(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "osk_live_abcdef", "not-a-jwt", "two.parts"} {
		tl := auth.NewTokenLogin(
			verifyFn(func(context.Context, string) (session.Principal, error) {
				t.Fatalf("verifier called for raw=%q", raw)
				return session.Principal{}, nil
			}),
			func(context.Context, auth.EnsureClaims) (*auth.UserRef, bool, error) {
				t.Fatal("ensureFromOIDC should not be called")
				return nil, false, nil
			},
			func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
				t.Fatal("memberships should not be called")
				return nil, nil
			},
			newTestLogger(),
			noopUnexpected(),
		)
		_, err := tl.Authenticate(t.Context(), raw)
		if !authn.IsUnauthorizedError(err) {
			t.Fatalf("raw=%q: got %T: %v, want *authn.UnauthorizedError", raw, err, err)
		}
	}
}

func TestTokenLogin_IDPOrgIDSelectsTheOrg(t *testing.T) {
	t.Parallel()
	orgID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	tl := auth.NewTokenLogin(
		verifyFn(func(_ context.Context, raw string) (session.Principal, error) {
			if raw != testJWT {
				t.Errorf("raw=%q want %q", raw, testJWT)
			}
			return verifiedPrincipal(orgID.String()), nil
		}),
		ensureUser(userID),
		func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
			t.Fatal("memberships must not be consulted when org_id is present")
			return nil, nil
		},
		newTestLogger(),
		noopUnexpected(),
	)

	p, err := tl.Authenticate(t.Context(), testJWT)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.ActiveOrgID != orgID {
		t.Errorf("ActiveOrgID=%s want %s", p.ActiveOrgID, orgID)
	}
	if p.ActiveProjectID != uuid.Nil {
		t.Errorf("ActiveProjectID=%s want uuid.Nil", p.ActiveProjectID)
	}
	if p.UserID != userID {
		t.Errorf("UserID=%s want %s", p.UserID, userID)
	}
	if p.Source != session.SourceToken {
		t.Errorf("Source=%q want %q", p.Source, session.SourceToken)
	}
	if !slices.Equal(p.Scopes, authn.AllScopes()) {
		t.Errorf("Scopes=%v want %v", p.Scopes, authn.AllScopes())
	}
	if p.IDPOrgID != orgID.String() || p.IDPIssuer != "https://idp.example.com" || p.IDPSubject != "sub-1" {
		t.Errorf("idp fields not carried: %+v", p)
	}
	if p.Email != "member@example.com" || p.Locale != "en" {
		t.Errorf("user projection not carried: %+v", p)
	}
	if p.IDToken != "" {
		t.Errorf("IDToken must stay empty, got %q", p.IDToken)
	}
}

func TestTokenLogin_SingleMembershipSelectsTheOrg(t *testing.T) {
	t.Parallel()
	orgID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	var askedFor uuid.UUID
	tl := auth.NewTokenLogin(
		verifyFn(func(context.Context, string) (session.Principal, error) {
			return verifiedPrincipal(""), nil
		}),
		ensureUser(userID),
		func(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
			askedFor = id
			return []uuid.UUID{orgID}, nil
		},
		newTestLogger(),
		noopUnexpected(),
	)

	p, err := tl.Authenticate(t.Context(), testJWT)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if askedFor != userID {
		t.Errorf("memberships asked for %s want %s", askedFor, userID)
	}
	if p.ActiveOrgID != orgID {
		t.Errorf("ActiveOrgID=%s want %s", p.ActiveOrgID, orgID)
	}
	if p.ActiveProjectID != uuid.Nil {
		t.Errorf("ActiveProjectID=%s want uuid.Nil", p.ActiveProjectID)
	}
	if !slices.Equal(p.Scopes, authn.AllScopes()) {
		t.Errorf("Scopes=%v want %v", p.Scopes, authn.AllScopes())
	}
}

func TestTokenLogin_AmbiguousAndAbsentMembershipsRejected(t *testing.T) {
	t.Parallel()
	cases := map[string][]uuid.UUID{
		"two memberships": {uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())},
		"no membership":   {},
	}
	for name, orgs := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tl := auth.NewTokenLogin(
				verifyFn(func(context.Context, string) (session.Principal, error) {
					return verifiedPrincipal(""), nil
				}),
				ensureUser(uuid.Must(uuid.NewV7())),
				func(context.Context, uuid.UUID) ([]uuid.UUID, error) { return orgs, nil },
				newTestLogger(),
				noopUnexpected(),
			)
			p, err := tl.Authenticate(t.Context(), testJWT)
			if !auth.IsOrgUnresolvedError(err) {
				t.Fatalf("got %T: %v, want *auth.OrgUnresolvedError", err, err)
			}
			if p.ActiveOrgID != uuid.Nil {
				t.Errorf("rejected login leaked ActiveOrgID=%s", p.ActiveOrgID)
			}
			ae, ok := apperror.AsAppError(err)
			if !ok {
				t.Fatalf("AsAppError failed for %T", err)
			}
			if ae.Code() != apperror.CodeTenantMissing {
				t.Errorf("code=%q want %q", ae.Code(), apperror.CodeTenantMissing)
			}
		})
	}
}

func TestTokenLogin_UnparseableOrgIDRejected(t *testing.T) {
	t.Parallel()
	tl := auth.NewTokenLogin(
		verifyFn(func(context.Context, string) (session.Principal, error) {
			return verifiedPrincipal("not-a-uuid"), nil
		}),
		ensureUser(uuid.Must(uuid.NewV7())),
		func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
			t.Fatal("an asserted org_id must not fall back to membership inference")
			return nil, nil
		},
		newTestLogger(),
		noopUnexpected(),
	)
	if _, err := tl.Authenticate(t.Context(), testJWT); !auth.IsOrgUnresolvedError(err) {
		t.Fatalf("got %T: %v, want *auth.OrgUnresolvedError", err, err)
	}
}

func TestTokenLogin_VerifierErrorPropagates(t *testing.T) {
	t.Parallel()
	tl := auth.NewTokenLogin(
		verifyFn(func(context.Context, string) (session.Principal, error) {
			return session.Principal{}, &tokens.ExpiredTokenError{}
		}),
		func(context.Context, auth.EnsureClaims) (*auth.UserRef, bool, error) {
			t.Fatal("ensureFromOIDC must not run after a failed verification")
			return nil, false, nil
		},
		func(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil },
		newTestLogger(),
		noopUnexpected(),
	)
	_, err := tl.Authenticate(t.Context(), testJWT)
	if !tokens.IsExpiredTokenError(err) {
		t.Fatalf("got %T: %v, want *tokens.ExpiredTokenError", err, err)
	}
}

func TestTokenLogin_MissingClaimsRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		p     session.Principal
		claim string
	}{
		{name: "no issuer", p: session.Principal{IDPSubject: "sub", Email: "a@b.co"}, claim: "iss"},
		{name: "no subject", p: session.Principal{IDPIssuer: "iss", Email: "a@b.co"}, claim: "sub"},
		{name: "no email", p: session.Principal{IDPIssuer: "iss", IDPSubject: "sub"}, claim: "email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tl := auth.NewTokenLogin(
				verifyFn(func(context.Context, string) (session.Principal, error) { return tc.p, nil }),
				func(context.Context, auth.EnsureClaims) (*auth.UserRef, bool, error) {
					t.Fatal("ensureFromOIDC should not be called")
					return nil, false, nil
				},
				func(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil },
				newTestLogger(),
				noopUnexpected(),
			)
			_, err := tl.Authenticate(t.Context(), testJWT)
			var got *auth.OIDCClaimMissingError
			if !errors.As(err, &got) {
				t.Fatalf("got %T: %v, want *auth.OIDCClaimMissingError", err, err)
			}
			if got.Claim != tc.claim {
				t.Errorf("claim=%q want %q", got.Claim, tc.claim)
			}
		})
	}
}

func TestTokenLogin_UnknownSubjectSurfacesResolverError(t *testing.T) {
	t.Parallel()
	appErr := apperror.New(apperror.CodeUserNotFound, "no such user", codes.NotFound)
	tl := auth.NewTokenLogin(
		verifyFn(func(context.Context, string) (session.Principal, error) {
			return verifiedPrincipal(""), nil
		}),
		func(context.Context, auth.EnsureClaims) (*auth.UserRef, bool, error) { return nil, false, appErr },
		func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
			t.Fatal("memberships must not run for an unresolved user")
			return nil, nil
		},
		newTestLogger(),
		noopUnexpected(),
	)
	_, err := tl.Authenticate(t.Context(), testJWT)
	ae, ok := apperror.AsAppError(err)
	if !ok {
		t.Fatalf("got %T: %v, want an *apperror.AppError", err, err)
	}
	if ae.Code() != apperror.CodeUserNotFound {
		t.Errorf("code=%q want %q", ae.Code(), apperror.CodeUserNotFound)
	}
}

func TestTokenLogin_ResolverAndMembershipFailuresGoThroughUnexpected(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	cases := map[string]struct {
		ensure      auth.EnsureFromOIDCFn
		memberships auth.MembershipsFn
	}{
		"ensure fails": {
			ensure:      func(context.Context, auth.EnsureClaims) (*auth.UserRef, bool, error) { return nil, false, boom },
			memberships: func(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil },
		},
		"memberships fail": {
			ensure:      ensureUser(uuid.Must(uuid.NewV7())),
			memberships: func(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, boom },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tl := auth.NewTokenLogin(
				verifyFn(func(context.Context, string) (session.Principal, error) {
					return verifiedPrincipal(""), nil
				}),
				tc.ensure,
				tc.memberships,
				newTestLogger(),
				noopUnexpected(),
			)
			_, err := tl.Authenticate(t.Context(), testJWT)
			ae, ok := apperror.AsAppError(err)
			if !ok {
				t.Fatalf("got %T: %v, want an *apperror.AppError", err, err)
			}
			if ae.Code() != apperror.CodeUnexpectedError {
				t.Errorf("code=%q want %q", ae.Code(), apperror.CodeUnexpectedError)
			}
			if !errors.Is(err, boom) {
				t.Errorf("cause not preserved: %v", err)
			}
		})
	}
}

func TestTokenLogin_UnwiredDependenciesFailClosed(t *testing.T) {
	t.Parallel()
	for name, tl := range map[string]*auth.TokenLogin{
		"no verifier": auth.NewTokenLogin(nil, ensureUser(uuid.Nil), nil, newTestLogger(), noopUnexpected()),
		"no resolver": auth.NewTokenLogin(
			verifyFn(func(context.Context, string) (session.Principal, error) {
				return verifiedPrincipal(""), nil
			}), nil, nil, newTestLogger(), noopUnexpected()),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := tl.Authenticate(t.Context(), testJWT); !authn.IsUnauthorizedError(err) {
				t.Fatalf("got %T: %v, want *authn.UnauthorizedError", err, err)
			}
		})
	}
}

func TestTokenLogin_UnwiredMembershipsRejectsOrglessToken(t *testing.T) {
	t.Parallel()
	tl := auth.NewTokenLogin(
		verifyFn(func(context.Context, string) (session.Principal, error) {
			return verifiedPrincipal(""), nil
		}),
		ensureUser(uuid.Must(uuid.NewV7())),
		nil,
		newTestLogger(),
		noopUnexpected(),
	)
	if _, err := tl.Authenticate(t.Context(), testJWT); !auth.IsOrgUnresolvedError(err) {
		t.Fatalf("got %T: %v, want *auth.OrgUnresolvedError", err, err)
	}
}

func TestTokenLogin_CarriesTermsAndIssuedAt(t *testing.T) {
	t.Parallel()
	accepted := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	orgID := uuid.Must(uuid.NewV7())
	tl := auth.NewTokenLogin(
		verifyFn(func(context.Context, string) (session.Principal, error) {
			return verifiedPrincipal(orgID.String()), nil
		}),
		func(_ context.Context, claims auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return &auth.UserRef{
				ID:              uuid.Must(uuid.NewV7()),
				Email:           claims.Email,
				IsAdmin:         true,
				TermsAcceptedAt: &accepted,
			}, false, nil
		},
		func(context.Context, uuid.UUID) ([]uuid.UUID, error) { return nil, nil },
		newTestLogger(),
		noopUnexpected(),
	)
	p, err := tl.Authenticate(t.Context(), testJWT)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !p.TermsAcceptedAt.Equal(accepted) {
		t.Errorf("TermsAcceptedAt=%s want %s", p.TermsAcceptedAt, accepted)
	}
	if !p.IssuedAt.Equal(verifiedPrincipal("").IssuedAt) {
		t.Errorf("IssuedAt=%s want the verified token's issuance", p.IssuedAt)
	}
	if !p.IsAdmin {
		t.Error("IsAdmin not carried")
	}
}

func TestTokenLogin_SatisfiesAuthenticator(t *testing.T) {
	t.Parallel()
	var a authn.Authenticator = auth.NewTokenLogin(nil, nil, nil, newTestLogger(), noopUnexpected())
	if _, err := a.Authenticate(t.Context(), "osk_x"); err == nil {
		t.Fatal("expected the shape gate to reject an API key")
	}
}
