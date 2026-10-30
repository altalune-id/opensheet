package mcp_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type rejectingAuthenticator struct{ err error }

func (r rejectingAuthenticator) Authenticate(context.Context, string) (session.Principal, error) {
	return session.Principal{}, r.err
}

func TestAuthenticate_LogsEachChainAuthenticatorsCause(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	chain := authn.Chain{
		rejectingAuthenticator{err: errors.New("api key: no such key")},
		rejectingAuthenticator{err: errors.New("jwt: token audience is not this resource")},
	}
	guard := mcpinternal.Authenticate(chain, tokens.Resource{MetadataURL: "https://x/meta"}, log)
	h := guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("the guard admitted a request it must have rejected")
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer a.b.c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	logged := buf.String()
	for _, want := range []string{"api key: no such key", "jwt: token audience is not this resource"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("the log lost the cause %q — diagnosing a rejection is impossible:\n%s", want, logged)
		}
	}
	if body := rec.Body.String(); strings.Contains(body, "audience") {
		t.Fatalf("SECURITY: a cause leaked into the response body:\n%s", body)
	}
}

func TestAuthenticate_LogsADistinctReasonPerFailure(t *testing.T) {
	tests := []struct {
		name       string
		auth       authn.Authenticator
		header     string
		wantReason string
	}{
		{"no authenticator", nil, "Bearer a.b.c", "no authenticator configured"},
		{"absent credential", rejectingAuthenticator{err: &authn.UnauthorizedError{}}, "", "no bearer credential"},
		{"chain rejected", rejectingAuthenticator{err: &authn.UnauthorizedError{}}, "Bearer a.b.c", "every authenticator rejected"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			guard := mcpinternal.Authenticate(tc.auth, tokens.Resource{MetadataURL: "https://x/meta"}, log)
			h := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Fatal("the guard admitted a request it must have rejected")
			}))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := buf.String(); !strings.Contains(got, tc.wantReason) {
				t.Fatalf("log did not carry the reason %q:\n%s", tc.wantReason, got)
			}
			if body := rec.Body.String(); strings.Contains(body, tc.wantReason) {
				t.Fatalf("SECURITY: the reason leaked into the response body:\n%s", body)
			}
		})
	}
}

func TestAuthenticate_AnOutageAnswers503WithoutAChallenge(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	chain := authn.Chain{
		rejectingAuthenticator{err: &authn.UnauthorizedError{}},
		rejectingAuthenticator{err: &authn.UnavailableError{Cause: errors.New("issuer metadata: connection refused")}},
	}
	guard := mcpinternal.Authenticate(chain, tokens.Resource{MetadataURL: "https://x/meta"}, log)
	h := guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("the guard admitted a credential it could not check")
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer a.b.c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q; an outage must not tell the client to drop its token", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, apperror.CodeTokenUnavailable) {
		t.Fatalf("body = %s, want code %s", body, apperror.CodeTokenUnavailable)
	}
	if strings.Contains(body, "connection refused") {
		t.Fatalf("SECURITY: the cause leaked into the response body:\n%s", body)
	}
	logged := buf.String()
	if !strings.Contains(logged, "connection refused") || !strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, "credential check unavailable") {
		t.Fatalf("an outage must be logged at error with its cause:\n%s", logged)
	}
}

func TestAuthenticate_AnAPIKeyDuringAStoreOutageAnswers503(t *testing.T) {
	store := fakes.NewAPIKey()
	store.BySecretHashErr = errors.New("connection refused")
	chain := authn.Chain{apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())}
	guard := mcpinternal.Authenticate(chain, tokens.Resource{MetadataURL: "https://x/meta"}, nil)
	h := guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("the guard admitted a key it could not check")
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+apikey.DefaultPrefix+"whatever")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("status = %d, WWW-Authenticate = %q; want 503 with no challenge", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}
