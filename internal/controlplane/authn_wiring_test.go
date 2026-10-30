package controlplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	sheetv1connect "altalune.id/opensheet/gen/go/sheet/v1/sheetv1connect"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/testutil/fakes"
)

// TestServer_HandlerOptions_EnforcesKeyScopes proves handlerOptions wires the real authn.Interceptor, not just interceptor.Auth's JWT check.
func TestServer_HandlerOptions_EnforcesKeyScopes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	store := fakes.NewAPIKey()
	orgID, projectID := uuid.New(), uuid.New()
	now := time.Now().UTC()

	readKey, readPlain, err := apikey.Scheme{}.Mint(orgID, projectID, "read-only", []string{ScopePostsRead}, nil, nil, now)
	if err != nil {
		t.Fatalf("mint read-only key: %v", err)
	}
	store.Seed(readKey)

	adminKey, adminPlain, err := apikey.Scheme{}.Mint(orgID, projectID, "admin", []string{ScopePostsAdmin}, nil, nil, now)
	if err != nil {
		t.Fatalf("mint admin key: %v", err)
	}
	store.Seed(adminKey)

	keyAuthn := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())
	chain := authn.Chain{keyAuthn, tokens.NewAuthenticator(alwaysOKVerifier{}, authn.Scheme{Prefix: apikey.DefaultPrefix})}

	s := &Server{
		Kernel: &platform.Kernel{Log: log, Reporter: reporter},
		Authn:  chain,
	}

	// SheetService.Delete requires ScopeSheetsAdmin in controlplane.ScopeTable().
	const procedure = sheetv1connect.SheetServiceDeleteProcedure
	var gotPrincipal session.Principal
	handler := connect.NewUnaryHandlerSimple(
		procedure,
		func(ctx context.Context, _ *sheetv1.DeleteRequest) (*sheetv1.DeleteResponse, error) {
			gotPrincipal = session.PrincipalFrom(ctx)
			return &sheetv1.DeleteResponse{}, nil
		},
		s.handlerOptions()...,
	)
	mux := http.NewServeMux()
	mux.Handle(procedure, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client := connect.NewClient[sheetv1.DeleteRequest, sheetv1.DeleteResponse](http.DefaultClient, ts.URL+procedure)

	t.Run("read-only key is denied", func(t *testing.T) {
		req := connect.NewRequest(&sheetv1.DeleteRequest{SheetId: uuid.NewString()})
		req.Header().Set("Authorization", "Bearer "+readPlain)

		_, err := client.CallUnary(t.Context(), req)
		if err == nil {
			t.Fatal("expected the read-only key to be denied, got a successful delete")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want %v (denial: %v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
		}
	})

	t.Run("admin key succeeds", func(t *testing.T) {
		req := connect.NewRequest(&sheetv1.DeleteRequest{SheetId: uuid.NewString()})
		req.Header().Set("Authorization", "Bearer "+adminPlain)

		if _, err := client.CallUnary(t.Context(), req); err != nil {
			t.Fatalf("expected the admin-scoped key to succeed, got: %v", err)
		}
		if gotPrincipal.Source != session.SourceAPIKey {
			t.Errorf("principal source = %q, want %q", gotPrincipal.Source, session.SourceAPIKey)
		}
		if !slices.Contains(gotPrincipal.Scopes, ScopePostsAdmin) {
			t.Errorf("principal scopes = %v, missing %q", gotPrincipal.Scopes, ScopePostsAdmin)
		}
	})
}

// TestServer_HandlerOptions_RejectsUnscopedProcedure proves a procedure absent from ScopeTable denies every API-key caller.
func TestServer_HandlerOptions_RejectsUnscopedProcedure(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	store := fakes.NewAPIKey()
	key, plain, err := apikey.Scheme{}.MintOrg(uuid.New(), "everything", authn.AllScopes(), apikey.ProjectGrant{All: true}, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	store.Seed(key)

	chain := authn.Chain{apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()), tokens.NewAuthenticator(alwaysOKVerifier{}, authn.Scheme{Prefix: apikey.DefaultPrefix})}
	s := &Server{
		Kernel: &platform.Kernel{Log: log, Reporter: reporter},
		Authn:  chain,
	}

	const procedure = "/test.UndeclaredService/DoSomething"
	handler := connect.NewUnaryHandlerSimple(
		procedure,
		func(_ context.Context, _ *sheetv1.DeleteRequest) (*sheetv1.DeleteResponse, error) {
			return &sheetv1.DeleteResponse{}, nil
		},
		s.handlerOptions()...,
	)
	mux := http.NewServeMux()
	mux.Handle(procedure, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client := connect.NewClient[sheetv1.DeleteRequest, sheetv1.DeleteResponse](http.DefaultClient, ts.URL+procedure)
	req := connect.NewRequest(&sheetv1.DeleteRequest{SheetId: uuid.NewString()})
	req.Header().Set("Authorization", "Bearer "+plain)

	if _, err := client.CallUnary(t.Context(), req); err == nil {
		t.Fatal("expected an undeclared procedure to deny a key with every scope, got success")
	}
}

// TestServer_HandlerOptions_AcceptsAnyConfiguredKeyPrefix proves the control-plane shape gate and the resolver share one prefix.
func TestServer_HandlerOptions_AcceptsAnyConfiguredKeyPrefix(t *testing.T) {
	for _, configured := range []string{"", "key_", "ak_", "sk_live_"} {
		t.Run("prefix="+configured, func(t *testing.T) {
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			scheme := apikey.NewScheme(configured)
			store := fakes.NewAPIKey()

			key, plain, err := scheme.Mint(uuid.New(), uuid.New(), "probe", []string{ScopePostsAdmin}, nil, nil, time.Now().UTC())
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			store.Seed(key)

			keyAuthn := apikey.NewAuthenticator(store, nil, scheme, fakes.NewMembers())
			s := &Server{
				Kernel: &platform.Kernel{Log: log, Reporter: apperror.NewReporter(log, false)},
				Authn:  authn.Chain{keyAuthn},
			}

			const procedure = sheetv1connect.SheetServiceDeleteProcedure
			mux := http.NewServeMux()
			mux.Handle(procedure, connect.NewUnaryHandlerSimple(
				procedure,
				func(_ context.Context, _ *sheetv1.DeleteRequest) (*sheetv1.DeleteResponse, error) {
					return &sheetv1.DeleteResponse{}, nil
				},
				s.handlerOptions()...,
			))
			ts := httptest.NewServer(mux)
			t.Cleanup(ts.Close)

			req := connect.NewRequest(&sheetv1.DeleteRequest{SheetId: uuid.NewString()})
			req.Header().Set("Authorization", "Bearer "+plain)
			client := connect.NewClient[sheetv1.DeleteRequest, sheetv1.DeleteResponse](http.DefaultClient, ts.URL+procedure)
			if _, err := client.CallUnary(t.Context(), req); err != nil {
				t.Fatalf("api.keyPrefix %q: a key this deployment minted was refused at the boundary: %v", configured, err)
			}
		})
	}
}

type scopedTokenVerifier struct{ scopes []string }

func (v scopedTokenVerifier) Verify(context.Context, string) (session.Principal, error) {
	return session.Principal{Source: session.SourceToken, UserID: uuid.New(), Scopes: v.scopes}, nil
}

// TestServer_HandlerOptions_EnforcesTokenScopes proves a token principal is held to the same scope table as a key.
func TestServer_HandlerOptions_EnforcesTokenScopes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)
	const procedure = sheetv1connect.SheetServiceDeleteProcedure

	for _, tc := range []struct {
		name   string
		scopes []string
		want   connect.Code
	}{
		{"a token without the scope is denied", []string{authn.ScopeSheetsRead}, connect.CodePermissionDenied},
		{"a token with the scope is admitted", []string{authn.ScopeSheetsAdmin}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{
				Kernel: &platform.Kernel{Log: log, Reporter: reporter},
				Authn:  authn.Chain{tokens.NewAuthenticator(scopedTokenVerifier{scopes: tc.scopes}, authn.Scheme{Prefix: apikey.DefaultPrefix})},
			}
			handler := connect.NewUnaryHandlerSimple(procedure,
				func(context.Context, *sheetv1.DeleteRequest) (*sheetv1.DeleteResponse, error) {
					return &sheetv1.DeleteResponse{}, nil
				}, s.handlerOptions()...)
			mux := http.NewServeMux()
			mux.Handle(procedure, handler)
			ts := httptest.NewServer(mux)
			t.Cleanup(ts.Close)

			client := connect.NewClient[sheetv1.DeleteRequest, sheetv1.DeleteResponse](http.DefaultClient, ts.URL+procedure)
			req := connect.NewRequest(&sheetv1.DeleteRequest{ProjectId: uuid.NewString(), SheetId: uuid.NewString()})
			req.Header().Set("Authorization", "Bearer aaa.bbb.ccc")
			_, err := client.CallUnary(t.Context(), req)
			if tc.want == 0 {
				if err != nil {
					t.Fatalf("a token holding the scope was refused: %v", err)
				}
				return
			}
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", connect.CodeOf(err), tc.want, err)
			}
		})
	}
}

type failingTokenVerifier struct{ err error }

func (v failingTokenVerifier) Verify(context.Context, string) (session.Principal, error) {
	return session.Principal{}, v.err
}

// TestServer_HandlerOptions_AnOutageIsUnavailableNotUnauthenticated proves a client keeps its token through an issuer or database outage.
func TestServer_HandlerOptions_AnOutageIsUnavailableNotUnauthenticated(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	resource, err := tokens.NewResource("https://app.example.com/api")
	if err != nil {
		t.Fatal(err)
	}
	const procedure = sheetv1connect.SheetServiceDeleteProcedure
	scheme := authn.Scheme{Prefix: apikey.DefaultPrefix}
	downStore := fakes.NewAPIKey()
	downStore.BySecretHashErr = errors.New("connection refused")
	keyAuthn := apikey.NewAuthenticator(downStore, nil, apikey.Scheme{}, fakes.NewMembers())
	tokenChain := func(err error) authn.Chain {
		return authn.Chain{keyAuthn, tokens.NewAuthenticator(failingTokenVerifier{err: err}, scheme)}
	}

	for _, tc := range []struct {
		name          string
		chain         authn.Chain
		bearer        string
		want          connect.Code
		wantChallenge bool
	}{
		{"an issuer outage", tokenChain(&authn.UnavailableError{Cause: errors.New("issuer unreachable")}), "aaa.bbb.ccc", connect.CodeUnavailable, false},
		{"an api key during a store outage", tokenChain(nil), apikey.DefaultPrefix + "whatever", connect.CodeUnavailable, false},
		{"a refused token", tokenChain(&tokens.InvalidTokenError{Reason: "signature"}), "aaa.bbb.ccc", connect.CodeUnauthenticated, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{
				Kernel:   &platform.Kernel{Log: log, Reporter: apperror.NewReporter(log, false)},
				Authn:    tc.chain,
				Resource: &resource,
			}
			mux := http.NewServeMux()
			mux.Handle(procedure, connect.NewUnaryHandlerSimple(procedure,
				func(context.Context, *sheetv1.DeleteRequest) (*sheetv1.DeleteResponse, error) {
					t.Fatal("the handler ran for a credential that was not verified")
					return nil, nil
				}, s.handlerOptions()...))
			ts := httptest.NewServer(mux)
			t.Cleanup(ts.Close)

			client := connect.NewClient[sheetv1.DeleteRequest, sheetv1.DeleteResponse](http.DefaultClient, ts.URL+procedure)
			req := connect.NewRequest(&sheetv1.DeleteRequest{ProjectId: uuid.NewString(), SheetId: uuid.NewString()})
			req.Header().Set("Authorization", "Bearer "+tc.bearer)
			_, err := client.CallUnary(t.Context(), req)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", connect.CodeOf(err), tc.want, err)
			}
			var cerr *connect.Error
			if !errors.As(err, &cerr) {
				t.Fatalf("err = %T, want *connect.Error", err)
			}
			if got := cerr.Meta().Get("WWW-Authenticate") != ""; got != tc.wantChallenge {
				t.Fatalf("WWW-Authenticate present = %v, want %v", got, tc.wantChallenge)
			}
		})
	}
}

// TestServer_HandlerOptions_ChallengesFollowRFC6750 proves the 401 says invalid_token only for a presented credential and the 403 names a scope only to a token missing a real one.
func TestServer_HandlerOptions_ChallengesFollowRFC6750(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	resource, err := tokens.NewResource("https://app.example.com/api")
	if err != nil {
		t.Fatal(err)
	}
	store := fakes.NewAPIKey()
	readKey, readPlain, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "read-only", []string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	store.Seed(readKey)
	scheme := authn.Scheme{Prefix: apikey.DefaultPrefix}
	keyAuthn := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())
	readToken := authn.Chain{keyAuthn, tokens.NewAuthenticator(scopedTokenVerifier{scopes: []string{authn.ScopeSheetsRead}}, scheme)}
	refused := authn.Chain{keyAuthn, tokens.NewAuthenticator(failingTokenVerifier{err: &tokens.InvalidTokenError{Reason: "signature"}}, scheme)}

	for _, tc := range []struct {
		name      string
		chain     authn.Chain
		procedure string
		bearer    string
		want      connect.Code
		challenge string
	}{
		{"no credential", refused, sheetv1connect.SheetServiceDeleteProcedure, "", connect.CodeUnauthenticated, resource.Challenge()},
		{"a refused token", refused, sheetv1connect.SheetServiceDeleteProcedure, "aaa.bbb.ccc", connect.CodeUnauthenticated, resource.InvalidToken()},
		{"a token missing a scope", readToken, sheetv1connect.SheetServiceDeleteProcedure, "aaa.bbb.ccc", connect.CodePermissionDenied, resource.InsufficientScope(authn.ScopeSheetsAdmin)},
		{"a key missing a scope", readToken, sheetv1connect.SheetServiceDeleteProcedure, readPlain, connect.CodePermissionDenied, ""},
		{"a token on an undeclared procedure", readToken, "/test.UndeclaredService/DoSomething", "aaa.bbb.ccc", connect.CodePermissionDenied, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{
				Kernel:   &platform.Kernel{Log: log, Reporter: apperror.NewReporter(log, false)},
				Authn:    tc.chain,
				Resource: &resource,
			}
			mux := http.NewServeMux()
			mux.Handle(tc.procedure, connect.NewUnaryHandlerSimple(tc.procedure,
				func(context.Context, *sheetv1.DeleteRequest) (*sheetv1.DeleteResponse, error) {
					t.Fatal("the handler ran for a refused call")
					return nil, nil
				}, s.handlerOptions()...))
			ts := httptest.NewServer(mux)
			t.Cleanup(ts.Close)

			client := connect.NewClient[sheetv1.DeleteRequest, sheetv1.DeleteResponse](http.DefaultClient, ts.URL+tc.procedure)
			req := connect.NewRequest(&sheetv1.DeleteRequest{SheetId: uuid.NewString()})
			if tc.bearer != "" {
				req.Header().Set("Authorization", "Bearer "+tc.bearer)
			}
			_, err := client.CallUnary(t.Context(), req)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", connect.CodeOf(err), tc.want, err)
			}
			var cerr *connect.Error
			if !errors.As(err, &cerr) {
				t.Fatalf("err = %T, want *connect.Error", err)
			}
			if got := cerr.Meta().Get("WWW-Authenticate"); got != tc.challenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, tc.challenge)
			}
		})
	}
}
