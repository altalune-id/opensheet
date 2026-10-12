package controlplane

import (
	"context"
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
	chain := authn.Chain{keyAuthn, tokens.NewAuthenticator(alwaysOKVerifier{})}

	s := &Server{
		Kernel:    &platform.Kernel{Log: log, Reporter: reporter},
		Authn:     chain,
		KeyPrefix: apikey.DefaultPrefix,
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

	chain := authn.Chain{apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()), tokens.NewAuthenticator(alwaysOKVerifier{})}
	s := &Server{
		Kernel:    &platform.Kernel{Log: log, Reporter: reporter},
		Authn:     chain,
		KeyPrefix: apikey.DefaultPrefix,
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
				Kernel:    &platform.Kernel{Log: log, Reporter: apperror.NewReporter(log, false)},
				Authn:     authn.Chain{keyAuthn},
				KeyPrefix: keyAuthn.Scheme().Prefix(),
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
