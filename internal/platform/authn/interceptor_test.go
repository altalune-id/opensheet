package authn_test

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

type stubAuth struct{ p session.Principal }

func (s stubAuth) Authenticate(_ context.Context, _ string) (session.Principal, error) {
	return s.p, nil
}

func keyPrincipal(scopes ...string) session.Principal {
	return session.Principal{
		Source:      session.SourceAPIKey,
		ActiveOrgID: uuid.New(),
		Scopes:      scopes,
	}
}

type fakeReq struct {
	connect.AnyRequest
	procedure string
	header    http.Header
}

func (f *fakeReq) Spec() connect.Spec  { return connect.Spec{Procedure: f.procedure} }
func (f *fakeReq) Header() http.Header { return f.header }

func req(procedure, credential string) *fakeReq {
	h := http.Header{}
	if credential != "" {
		h.Set("Authorization", "Bearer "+credential)
	}
	return &fakeReq{procedure: procedure, header: h}
}

func TestInterceptorDeniesMethodMissingFromTable(t *testing.T) {
	ic := authn.Interceptor(
		authn.Chain{stubAuth{p: keyPrincipal(authn.ScopeSheetsRead)}},
		authn.Scheme{Prefix: "key_"},
		authn.ScopeTable{"/blog.v1.BlogService/ListPosts": authn.ScopeSheetsRead},
	)

	called := false
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return nil, nil
	})

	// A method absent from the table must never reach the handler.
	_, err := ic(next)(t.Context(), req("/blog.v1.BlogService/DeletePost", "key_abc"))
	if err == nil {
		t.Fatal("undeclared method was admitted; the table must fail closed")
	}
	if called {
		t.Fatal("handler ran for an undeclared method")
	}
}

func TestInterceptorDeniesInsufficientScope(t *testing.T) {
	ic := authn.Interceptor(
		authn.Chain{stubAuth{p: keyPrincipal(authn.ScopeSheetsRead)}},
		authn.Scheme{Prefix: "key_"},
		authn.ScopeTable{"/blog.v1.BlogService/DeletePost": authn.ScopeSheetsAdmin},
	)
	next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		t.Fatal("handler ran without the required scope")
		return nil, nil
	})

	_, err := ic(next)(t.Context(), req("/blog.v1.BlogService/DeletePost", "key_abc"))
	if !authn.IsInsufficientScopeError(err) {
		t.Fatalf("err = %v, want *InsufficientScopeError", err)
	}
}

func TestInterceptorSkipsScopeCheckForUserPrincipal(t *testing.T) {
	user := session.Principal{UserID: uuid.New()}
	ic := authn.Interceptor(
		authn.Chain{stubAuth{p: user}},
		authn.Scheme{Prefix: "key_"},
		authn.ScopeTable{},
	)
	called := false
	next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return nil, nil
	})

	if _, err := ic(next)(t.Context(), req("/any/Method", "aaa.bbb.ccc")); err != nil {
		t.Fatalf("user principal rejected: %v", err)
	}
	if !called {
		t.Fatal("user principal did not reach the handler")
	}
}
