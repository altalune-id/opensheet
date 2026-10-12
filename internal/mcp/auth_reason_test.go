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

	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
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
	guard := mcpinternal.Authenticate(chain, authn.Scheme{Prefix: "key_"}, "https://x/meta", log)
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
		{"unrecognized shape", rejectingAuthenticator{err: &authn.UnauthorizedError{}}, "Bearer opaque-token", "neither the api key prefix nor the JWT shape"},
		{"chain rejected", rejectingAuthenticator{err: &authn.UnauthorizedError{}}, "Bearer a.b.c", "every authenticator rejected"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			guard := mcpinternal.Authenticate(tc.auth, authn.Scheme{Prefix: "key_"}, "https://x/meta", log)
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
