package tokens_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tokens"
)

func TestMissingAuthError_ErrorAndAppError(t *testing.T) {
	e := &tokens.MissingAuthError{}
	if e.Error() != "tokens: missing Authorization header" {
		t.Errorf("Error()=%q", e.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeUnauthenticated {
		t.Errorf("Code()=%q want %q", ae.Code(), apperror.CodeUnauthenticated)
	}
	if ae.GRPCCode() != codes.Unauthenticated {
		t.Errorf("GRPCCode()=%v want Unauthenticated", ae.GRPCCode())
	}
	if len(ae.Details()) != 1 {
		t.Errorf("Details() len=%d want 1", len(ae.Details()))
	}
}

func TestIsMissingAuthError_UnwrapsThroughFmtErrorf(t *testing.T) {
	wrapped := fmt.Errorf("service: %w", &tokens.MissingAuthError{})
	if !tokens.IsMissingAuthError(wrapped) {
		t.Fatal("IsMissingAuthError should walk fmt.Errorf %w chains")
	}
}

func TestIsMissingAuthError_False(t *testing.T) {
	if tokens.IsMissingAuthError(errors.New("nope")) {
		t.Fatal("plain error must not match")
	}
	if tokens.IsMissingAuthError(nil) {
		t.Fatal("nil must not match")
	}
}

func TestBadSchemeError_MessageAndAppError(t *testing.T) {
	e := &tokens.BadSchemeError{Scheme: "Basic"}
	if got := e.Error(); got != `tokens: Authorization must use Bearer scheme (got "Basic")` {
		t.Errorf("Error()=%q", got)
	}
	empty := &tokens.BadSchemeError{}
	if empty.Error() != "tokens: Authorization must use Bearer scheme" {
		t.Errorf("empty scheme Error()=%q", empty.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeUnauthenticated || ae.GRPCCode() != codes.Unauthenticated {
		t.Errorf("BadSchemeError.ToAppError code/grpc mismatch: %v / %v", ae.Code(), ae.GRPCCode())
	}
}

func TestIsBadSchemeError_Chain(t *testing.T) {
	e := fmt.Errorf("wrap: %w", &tokens.BadSchemeError{Scheme: "Digest"})
	if !tokens.IsBadSchemeError(e) {
		t.Fatal("IsBadSchemeError should walk %w chains")
	}
	if tokens.IsBadSchemeError(errors.New("other")) {
		t.Fatal("plain error must not match")
	}
}

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
		{"missing", &tokens.MissingAuthError{}, apperror.CodeUnauthenticated},
		{"bad-scheme", &tokens.BadSchemeError{Scheme: "Basic"}, apperror.CodeUnauthenticated},
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
