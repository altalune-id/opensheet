package tokens_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tokens"
)

func TestInvalidTokenError_UnwrapAndReason(t *testing.T) {
	cause := errors.New("signature invalid")
	e := &tokens.InvalidTokenError{Reason: "bad sig", Cause: cause}
	if e.Error() != "tokens: invalid token: bad sig" {
		t.Errorf("Error()=%q", e.Error())
	}
	if !errors.Is(e, cause) {
		t.Errorf("errors.Is should walk Cause")
	}
	empty := &tokens.InvalidTokenError{}
	if empty.Error() != "tokens: invalid token" {
		t.Errorf("empty reason Error()=%q", empty.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeUnauthenticated {
		t.Errorf("Code()=%q", ae.Code())
	}
}

func TestIsInvalidTokenError_Chain(t *testing.T) {
	wrapped := fmt.Errorf("layer: %w", &tokens.InvalidTokenError{Reason: "x"})
	if !tokens.IsInvalidTokenError(wrapped) {
		t.Fatal("IsInvalidTokenError should walk %w chains")
	}
}

func TestExpiredTokenError_UnwrapAndAppError(t *testing.T) {
	cause := errors.New("token exp 2020")
	e := &tokens.ExpiredTokenError{Cause: cause}
	if e.Error() != "tokens: token expired: token exp 2020" {
		t.Errorf("Error()=%q", e.Error())
	}
	if !errors.Is(e, cause) {
		t.Errorf("errors.Is should walk Cause")
	}
	empty := &tokens.ExpiredTokenError{}
	if empty.Error() != "tokens: token expired" {
		t.Errorf("empty Error()=%q", empty.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeTokenExpired {
		t.Errorf("Code()=%q want %q", ae.Code(), apperror.CodeTokenExpired)
	}
	if ae.GRPCCode() != codes.Unauthenticated {
		t.Errorf("GRPCCode()=%v want Unauthenticated", ae.GRPCCode())
	}
}

func TestIsExpiredTokenError_Chain(t *testing.T) {
	wrapped := fmt.Errorf("layer: %w", &tokens.ExpiredTokenError{})
	if !tokens.IsExpiredTokenError(wrapped) {
		t.Fatal("IsExpiredTokenError should walk %w chains")
	}
}

func TestAsAppError_DiscoversAllTypedTokenErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"invalid", &tokens.InvalidTokenError{Reason: "x"}, apperror.CodeUnauthenticated},
		{"expired", &tokens.ExpiredTokenError{}, apperror.CodeTokenExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("layer: %w", tc.err)
			ae, ok := apperror.AsAppError(wrapped)
			if !ok {
				t.Fatalf("AsAppError should discover via ToAppError, got err=%v", tc.err)
			}
			if ae.Code() != tc.want {
				t.Errorf("Code()=%q want %q", ae.Code(), tc.want)
			}
		})
	}
}
