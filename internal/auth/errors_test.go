package auth_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
)

func TestInvalidCredentialsError(t *testing.T) {
	t.Parallel()
	err := &auth.InvalidCredentialsError{}
	if err.Error() != "auth: invalid credentials" {
		t.Errorf("Error()=%q", err.Error())
	}
	ae := err.ToAppError()
	if ae.Code() != apperror.CodeAuthInvalidCredentials {
		t.Errorf("code=%q", ae.Code())
	}
	if ae.GRPCCode() != codes.Unauthenticated {
		t.Errorf("grpcCode=%v", ae.GRPCCode())
	}
	if !auth.IsInvalidCredentialsError(err) {
		t.Error("IsInvalidCredentialsError direct")
	}
	if !auth.IsInvalidCredentialsError(fmt.Errorf("wrap: %w", err)) {
		t.Error("IsInvalidCredentialsError wrapped")
	}
	if auth.IsInvalidCredentialsError(errors.New("boom")) {
		t.Error("false positive")
	}
}

func TestOIDCUnavailableError(t *testing.T) {
	t.Parallel()
	err := &auth.OIDCUnavailableError{}
	if err.Error() != "auth: oidc: unavailable" {
		t.Errorf("Error()=%q", err.Error())
	}
	ae := err.ToAppError()
	if ae.Code() != apperror.CodeAuthOIDCUnavailable {
		t.Errorf("code=%q", ae.Code())
	}
	if ae.GRPCCode() != codes.FailedPrecondition {
		t.Errorf("grpcCode=%v", ae.GRPCCode())
	}
	if !auth.IsOIDCUnavailableError(err) {
		t.Error("IsOIDCUnavailableError direct")
	}
	if !auth.IsOIDCUnavailableError(fmt.Errorf("wrap: %w", err)) {
		t.Error("IsOIDCUnavailableError wrapped")
	}
	if auth.IsOIDCUnavailableError(errors.New("boom")) {
		t.Error("false positive")
	}
}

func TestOIDCClaimMissingError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		claim   string
		wantMsg string
	}{
		{name: "with claim", claim: "sub", wantMsg: "auth: oidc: claim missing: sub"},
		{name: "empty claim", claim: "", wantMsg: "auth: oidc: claim missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &auth.OIDCClaimMissingError{Claim: tc.claim}
			if err.Error() != tc.wantMsg {
				t.Errorf("Error()=%q want %q", err.Error(), tc.wantMsg)
			}
			ae := err.ToAppError()
			if ae.Code() != apperror.CodeAuthOIDCClaimMissing {
				t.Errorf("code=%q", ae.Code())
			}
			if ae.GRPCCode() != codes.InvalidArgument {
				t.Errorf("grpcCode=%v", ae.GRPCCode())
			}
			if !auth.IsOIDCClaimMissingError(err) {
				t.Error("IsOIDCClaimMissingError direct")
			}
			if !auth.IsOIDCClaimMissingError(fmt.Errorf("wrap: %w", err)) {
				t.Error("IsOIDCClaimMissingError wrapped")
			}
		})
	}
	if auth.IsOIDCClaimMissingError(errors.New("boom")) {
		t.Error("false positive")
	}
}

func TestNotInvitedError(t *testing.T) {
	t.Parallel()
	err := &auth.NotInvitedError{Email: "a@b.co"}
	if err.Error() != "auth: not invited: email=a@b.co" {
		t.Errorf("Error()=%q", err.Error())
	}
	if (&auth.NotInvitedError{}).Error() != "auth: not invited" {
		t.Error("empty email message")
	}
	ae := err.ToAppError()
	if ae.Code() != apperror.CodeUserNotInvited {
		t.Errorf("code=%q", ae.Code())
	}
	if ae.GRPCCode() != codes.PermissionDenied {
		t.Errorf("grpcCode=%v", ae.GRPCCode())
	}
	if !auth.IsNotInvitedError(err) {
		t.Error("IsNotInvitedError direct")
	}
	if !auth.IsNotInvitedError(fmt.Errorf("wrap: %w", err)) {
		t.Error("IsNotInvitedError wrapped")
	}
	if auth.IsNotInvitedError(errors.New("boom")) {
		t.Error("false positive")
	}
}
