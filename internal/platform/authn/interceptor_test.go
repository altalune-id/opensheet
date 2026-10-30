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

func tokenPrincipal(scopes ...string) session.Principal {
	return session.Principal{Source: session.SourceToken, UserID: uuid.New(), Scopes: scopes}
}

func TestInterceptorEnforcesScopesOnTokenPrincipal(t *testing.T) {
	table := authn.ScopeTable{"/blog.v1.BlogService/ListPosts": authn.ScopeSheetsRead}
	tests := []struct {
		name      string
		principal session.Principal
		procedure string
		wantErr   bool
	}{
		{"token with the scope passes", tokenPrincipal(authn.ScopeSheetsRead), "/blog.v1.BlogService/ListPosts", false},
		{"token without the scope is denied", tokenPrincipal(authn.ScopeMembersRead), "/blog.v1.BlogService/ListPosts", true},
		{"token on an undeclared procedure is denied", tokenPrincipal(authn.ScopeSheetsRead), "/blog.v1.BlogService/DeletePost", true},
		{"a principal with no source is denied without the scope", session.Principal{UserID: uuid.New()}, "/blog.v1.BlogService/ListPosts", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ic := authn.Interceptor(authn.Chain{stubAuth{p: tt.principal}}, table)
			called := false
			next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
				called = true
				return nil, nil
			})
			_, err := ic(next)(t.Context(), req(tt.procedure, "aaa.bbb.ccc"))
			if tt.wantErr {
				if !authn.IsInsufficientScopeError(err) || called {
					t.Fatalf("err = %v, called = %v; want *InsufficientScopeError before the handler", err, called)
				}
				return
			}
			if err != nil || !called {
				t.Fatalf("err = %v, called = %v; want the handler to run", err, called)
			}
		})
	}
}

func TestInterceptorSkipsScopeCheckForSessionPrincipal(t *testing.T) {
	for _, src := range []session.Source{session.SourceOIDC, session.SourceLocal, session.SourceGenesis} {
		t.Run(string(src), func(t *testing.T) {
			ic := authn.Interceptor(authn.Chain{stubAuth{p: session.Principal{UserID: uuid.New(), Source: src}}}, authn.ScopeTable{})
			called := false
			next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
				called = true
				return nil, nil
			})
			if _, err := ic(next)(t.Context(), req("/any/Method", "aaa.bbb.ccc")); err != nil || !called {
				t.Fatalf("session principal: err = %v, called = %v", err, called)
			}
		})
	}
}
