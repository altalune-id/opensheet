package tokens_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tokens"
)

type stubVerifier struct {
	p   session.Principal
	err error
}

func (s stubVerifier) Verify(_ context.Context, _ string) (session.Principal, error) {
	return s.p, s.err
}

type emptyReq struct{}

func newReq() *connect.Request[emptyReq] {
	return connect.NewRequest(&emptyReq{})
}

func TestInterceptor_MissingHeader(t *testing.T) {
	inter := tokens.Interceptor(stubVerifier{})
	req := newReq()
	req.Header().Del("Authorization")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing Authorization header")
	}
	if !tokens.IsMissingAuthError(err) {
		t.Fatalf("expected *MissingAuthError in chain, got %T: %v", err, err)
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("expected connect Unauthenticated, got %v", err)
	}
}

func TestInterceptor_BadScheme(t *testing.T) {
	inter := tokens.Interceptor(stubVerifier{})
	req := newReq()
	req.Header().Set("Authorization", "Basic abc")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-Bearer scheme")
	}
	if !tokens.IsBadSchemeError(err) {
		t.Fatalf("expected *BadSchemeError in chain, got %T: %v", err, err)
	}
	var bad *tokens.BadSchemeError
	if !errors.As(err, &bad) {
		t.Fatalf("errors.As should discover *BadSchemeError")
	}
	if bad.Scheme != "Basic" {
		t.Errorf("Scheme=%q want %q", bad.Scheme, "Basic")
	}
}

func TestInterceptor_ValidToken(t *testing.T) {
	p := session.Principal{Email: "a@b"}
	inter := tokens.Interceptor(stubVerifier{p: p})
	req := newReq()
	req.Header().Set("Authorization", "Bearer xyz")
	var got session.Principal
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		got = session.PrincipalFrom(ctx)
		return connect.NewResponse(&struct{}{}), nil
	})
	if _, err := inter(next)(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got.Email != "a@b" {
		t.Errorf("principal not injected: %+v", got)
	}
}

func TestInterceptor_VerifierErr_TypedInvalidPropagates(t *testing.T) {
	inter := tokens.Interceptor(stubVerifier{err: &tokens.InvalidTokenError{Reason: "bad sig"}})
	req := newReq()
	req.Header().Set("Authorization", "Bearer xyz")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected verifier error to propagate")
	}
	if !tokens.IsInvalidTokenError(err) {
		t.Fatalf("expected *InvalidTokenError to walk connect.Error chain, got %T: %v", err, err)
	}
}

func TestInterceptor_VerifierErr_ExpiredPropagates(t *testing.T) {
	inter := tokens.Interceptor(stubVerifier{err: &tokens.ExpiredTokenError{}})
	req := newReq()
	req.Header().Set("Authorization", "Bearer xyz")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected verifier error to propagate")
	}
	if !tokens.IsExpiredTokenError(err) {
		t.Fatalf("expected *ExpiredTokenError to walk connect.Error chain, got %T: %v", err, err)
	}
}
