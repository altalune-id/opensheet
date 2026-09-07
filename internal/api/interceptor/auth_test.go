package interceptor_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"altalune.id/template/internal/api/interceptor"
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

func TestAuth_MissingHeader(t *testing.T) {
	inter := interceptor.Auth(stubVerifier{})
	req := newReq()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if !tokens.IsMissingAuthError(err) {
		t.Errorf("expected *MissingAuthError, got %T: %v", err, err)
	}
}

func TestAuth_BadScheme(t *testing.T) {
	inter := interceptor.Auth(stubVerifier{})
	req := newReq()
	req.Header().Set("Authorization", "Basic abc")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	var bad *tokens.BadSchemeError
	if !errors.As(err, &bad) {
		t.Fatalf("expected *BadSchemeError, got %T", err)
	}
	if bad.Scheme != "Basic" {
		t.Errorf("Scheme = %q, want Basic", bad.Scheme)
	}
}

func TestAuth_ValidToken_InjectsPrincipal(t *testing.T) {
	p := session.Principal{Email: "a@b"}
	inter := interceptor.Auth(stubVerifier{p: p})
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
		t.Errorf("principal not propagated: %+v", got)
	}
}

func TestAuth_VerifierExpired_PropagatesTyped(t *testing.T) {
	inter := interceptor.Auth(stubVerifier{err: &tokens.ExpiredTokenError{}})
	req := newReq()
	req.Header().Set("Authorization", "Bearer xyz")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	})
	_, err := inter(next)(context.Background(), req)
	if !tokens.IsExpiredTokenError(err) {
		t.Fatalf("expected *ExpiredTokenError, got %T: %v", err, err)
	}
}
