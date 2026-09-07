package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/platform/session"
)

func TestOIDCLogin_MissingClaims(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		claims auth.OIDCClaims
		claim  string
	}{
		{name: "missing issuer", claims: auth.OIDCClaims{Subject: "sub", Email: "a@b.co"}, claim: "iss"},
		{name: "missing subject", claims: auth.OIDCClaims{Issuer: "iss", Email: "a@b.co"}, claim: "sub"},
		{name: "missing email", claims: auth.OIDCClaims{Issuer: "iss", Subject: "sub"}, claim: "email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := auth.NewOIDCLogin(
				func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
					t.Fatal("ensureFromOIDC should not be called")
					return nil, false, nil
				},
				nil,
				newTestLogger(),
				noopUnexpected(),
			)
			_, err := o.Execute(context.Background(), tc.claims)
			if err == nil {
				t.Fatal("expected error")
			}
			if !auth.IsOIDCClaimMissingError(err) {
				t.Fatalf("got %T: %v", err, err)
			}
			var got *auth.OIDCClaimMissingError
			if !errors.As(err, &got) {
				t.Fatalf("expected concrete claim error")
			}
			if got.Claim != tc.claim {
				t.Errorf("claim=%q want %q", got.Claim, tc.claim)
			}
		})
	}
}

func TestOIDCLogin_Unavailable(t *testing.T) {
	t.Parallel()
	o := auth.NewOIDCLogin(nil, nil, newTestLogger(), noopUnexpected())
	_, err := o.Execute(context.Background(), auth.OIDCClaims{Issuer: "iss", Subject: "sub", Email: "a@b.co"})
	if !auth.IsOIDCUnavailableError(err) {
		t.Fatalf("got %T: %v", err, err)
	}
}

func TestOIDCLogin_EnsureError_Wraps(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("upstream boom")
	var routed bool
	o := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return nil, false, sentinel
		},
		nil,
		newTestLogger(),
		func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
			routed = true
			return apperror.New(apperror.CodeUnexpectedError, "unexpected", 0).WithCause(cause)
		},
	)
	_, err := o.Execute(context.Background(), auth.OIDCClaims{Issuer: "iss", Subject: "sub", Email: "a@b.co"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !routed {
		t.Error("unexpected func not invoked")
	}
}

func TestOIDCLogin_TypedEnsureErrorPropagates(t *testing.T) {
	t.Parallel()
	invited := &auth.NotInvitedError{Email: "a@b.co"}
	o := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return nil, false, invited
		},
		nil,
		newTestLogger(),
		noopUnexpected(),
	)
	_, err := o.Execute(context.Background(), auth.OIDCClaims{Issuer: "iss", Subject: "sub", Email: "a@b.co"})
	if _, ok := apperror.AsAppError(err); !ok {
		t.Fatalf("expected AppError wrapping typed producer, got %T: %v", err, err)
	}
}

func TestOIDCLogin_ExistingUser_NoOnboardWiredReturnsPrincipal(t *testing.T) {
	t.Parallel()
	u := &auth.UserRef{ID: uuid.New(), Email: "alice@example.com", Name: "Alice", Source: "oidc"}
	o := auth.NewOIDCLogin(
		func(_ context.Context, c auth.EnsureClaims) (*auth.UserRef, bool, error) {
			if c.Email != "alice@example.com" {
				t.Errorf("email=%q", c.Email)
			}
			return u, false, nil
		},
		nil,
		newTestLogger(),
		noopUnexpected(),
	)
	p, err := o.Execute(context.Background(), auth.OIDCClaims{
		Issuer: "https://idp", Subject: "sub-1", Email: "alice@example.com", Name: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != session.SourceOIDC {
		t.Errorf("source=%q", p.Source)
	}
	if p.IDPIssuer != "https://idp" || p.IDPSubject != "sub-1" {
		t.Errorf("idp fields not carried: %+v", p)
	}
	if p.UserID != u.ID {
		t.Errorf("userID mismatch")
	}
	if p.ActiveOrgID != uuid.Nil {
		t.Errorf("no onboarder wired: activeOrg=%v", p.ActiveOrgID)
	}
}

func TestOIDCLogin_CarriesTermsAcceptedAtIntoPrincipal(t *testing.T) {
	t.Parallel()
	accepted := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	u := &auth.UserRef{ID: uuid.New(), Email: "alice@example.com", Name: "Alice", Source: "oidc", TermsAcceptedAt: &accepted}
	o := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) { return u, false, nil },
		nil, newTestLogger(), noopUnexpected(),
	)
	p, err := o.Execute(context.Background(), auth.OIDCClaims{Issuer: "iss", Subject: "sub", Email: "alice@example.com", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.TermsAcceptedAt.Equal(accepted) {
		t.Errorf("TermsAcceptedAt lost across OIDC login: got %v, want %v", p.TermsAcceptedAt, accepted)
	}
}

func TestOIDCLogin_NewUser_Cloud_Onboards(t *testing.T) {
	t.Parallel()
	orgID, projectID := uuid.New(), uuid.New()
	u := &auth.UserRef{ID: uuid.New(), Email: "alice@example.com", Name: "Alice", Source: "oidc"}
	var onboarded bool
	o := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return u, true, nil
		},
		func(_ context.Context, req auth.OnboardRequest) (auth.OnboardResult, error) {
			onboarded = true
			if req.UserID != u.ID {
				t.Errorf("userID mismatch")
			}
			return auth.OnboardResult{OrgID: orgID, ProjectID: projectID}, nil
		},
		newTestLogger(),
		noopUnexpected(),
	)
	p, err := o.Execute(context.Background(), auth.OIDCClaims{
		Issuer: "https://idp", Subject: "sub", Email: "alice@example.com", Name: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !onboarded {
		t.Error("onboarder should have been invoked")
	}
	if p.ActiveOrgID != orgID || p.ActiveProjectID != projectID {
		t.Errorf("active ids not carried: %+v", p)
	}
}

func TestOIDCLogin_Selfhosted_NotInvited(t *testing.T) {
	t.Parallel()
	u := &auth.UserRef{ID: uuid.New(), Email: "stranger@example.com", Name: "Stranger", Source: "oidc"}
	o := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return u, true, nil
		},
		func(_ context.Context, req auth.OnboardRequest) (auth.OnboardResult, error) {
			return auth.OnboardResult{}, &auth.NotInvitedError{Email: req.Email}
		},
		newTestLogger(),
		noopUnexpected(),
	)
	_, err := o.Execute(context.Background(), auth.OIDCClaims{
		Issuer: "iss", Subject: "sub", Email: "stranger@example.com",
	})
	if err == nil {
		t.Fatal("expected NotInvitedError propagated as AppError")
	}
	if _, ok := apperror.AsAppError(err); !ok {
		t.Fatalf("expected typed AppError, got %T: %v", err, err)
	}
}

func TestOIDCLogin_OnboardUnexpectedRoutes(t *testing.T) {
	t.Parallel()
	u := &auth.UserRef{ID: uuid.New(), Email: "a@b.co", Name: "n", Source: "oidc"}
	var routed bool
	o := auth.NewOIDCLogin(
		func(_ context.Context, _ auth.EnsureClaims) (*auth.UserRef, bool, error) {
			return u, true, nil
		},
		func(_ context.Context, _ auth.OnboardRequest) (auth.OnboardResult, error) {
			return auth.OnboardResult{}, errors.New("driver: broken")
		},
		newTestLogger(),
		func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
			routed = true
			return apperror.New(apperror.CodeUnexpectedError, "unexpected", 0).WithCause(cause)
		},
	)
	_, err := o.Execute(context.Background(), auth.OIDCClaims{Issuer: "iss", Subject: "sub", Email: "a@b.co"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !routed {
		t.Error("unexpected func not invoked")
	}
}
