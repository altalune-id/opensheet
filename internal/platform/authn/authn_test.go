package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/session"
)

type stubAuth struct {
	accept string
	p      session.Principal
	calls  *int
}

func (s stubAuth) Authenticate(_ context.Context, raw string) (session.Principal, error) {
	if s.calls != nil {
		*s.calls++
	}
	if raw == s.accept {
		return s.p, nil
	}
	return session.Principal{}, &UnauthorizedError{}
}

func TestChain_ReturnsFirstSuccess(t *testing.T) {
	want := session.Principal{Email: "a@b.c"}
	c := Chain{
		stubAuth{accept: "nope"},
		stubAuth{accept: "yes", p: want},
	}
	got, err := c.Authenticate(context.Background(), "yes")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.Email != want.Email {
		t.Fatalf("Email = %q, want %q", got.Email, want.Email)
	}
}

func TestChain_StopsAtFirstSuccess(t *testing.T) {
	calls := 0
	c := Chain{
		stubAuth{accept: "yes", p: session.Principal{Email: "a@b.c"}},
		stubAuth{accept: "yes", calls: &calls},
	}
	if _, err := c.Authenticate(context.Background(), "yes"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if calls != 0 {
		t.Fatalf("second authenticator called %d times, want 0", calls)
	}
}

func TestChain_EmptyAndAllFailingReturnOpaqueError(t *testing.T) {
	for name, c := range map[string]Chain{
		"empty":       {},
		"all failing": {stubAuth{accept: "x"}, stubAuth{accept: "y"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.Authenticate(context.Background(), "zzz")
			if !IsUnauthorizedError(err) {
				t.Fatalf("error = %v, want UnauthorizedError", err)
			}
		})
	}
}

// SECURITY: missing, malformed, unknown, revoked and expired credentials must be indistinguishable.
func TestUnauthorizedError_MessageRevealsNothing(t *testing.T) {
	msg := (&UnauthorizedError{}).Error()
	for _, leak := range []string{"revoked", "expired", "unknown", "prefix", "malformed", "not found"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Errorf("error message %q leaks %q", msg, leak)
		}
	}
}

func TestUnauthorizedError_ToAppError(t *testing.T) {
	ae := (&UnauthorizedError{}).ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() != apperror.CodeAPIKeyUnauthorized {
		t.Errorf("Code() = %q, want %q", ae.Code(), apperror.CodeAPIKeyUnauthorized)
	}
	for _, leak := range []string{"revoked", "expired", "unknown", "prefix", "malformed", "not found"} {
		if strings.Contains(strings.ToLower(ae.Message()), leak) {
			t.Errorf("AppError message %q leaks %q", ae.Message(), leak)
		}
	}
}

func TestInsufficientScopeError(t *testing.T) {
	e := &InsufficientScopeError{Scope: ScopeCachePurge}
	if !strings.Contains(e.Error(), ScopeCachePurge) {
		t.Errorf("Error() = %q, want it to name the scope", e.Error())
	}
	if !IsInsufficientScopeError(e) {
		t.Error("IsInsufficientScopeError = false")
	}
	if IsInsufficientScopeError(&UnauthorizedError{}) {
		t.Error("IsInsufficientScopeError matched an unrelated error")
	}
	ae := e.ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() != apperror.CodeAPIKeyInsufficientScope {
		t.Errorf("Code() = %q, want %q", ae.Code(), apperror.CodeAPIKeyInsufficientScope)
	}
	if !strings.Contains(ae.Message(), ScopeCachePurge) {
		t.Errorf("AppError message = %q, want it to name the scope", ae.Message())
	}
}

func TestCredentialFrom(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		query   string
		want    string
	}{
		{"bearer", map[string]string{"Authorization": "Bearer osk_abc_def"}, "", "osk_abc_def"},
		{"lowercase bearer", map[string]string{"Authorization": "bearer osk_abc_def"}, "", "osk_abc_def"},
		{"x-api-key", map[string]string{"X-API-Key": "osk_abc_def"}, "", "osk_abc_def"},
		{"authorization wins", map[string]string{"Authorization": "Bearer aaa", "X-API-Key": "bbb"}, "", "aaa"},
		{"unknown scheme", map[string]string{"Authorization": "Basic dXNlcjpwYXNz"}, "", ""},
		{"none", nil, "", ""},
		// SECURITY: a credential in the query string leaks into access logs, proxy logs and Referer.
		{"query token rejected", nil, "?token=osk_abc_def", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x"+tc.query, nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := CredentialFrom(r); got != tc.want {
				t.Fatalf("CredentialFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLooks(t *testing.T) {
	tests := map[string]Shape{
		"osk_0123456789abcdef_secretsecret":        ShapeAPIKey,
		"eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.sig": ShapeJWT,
		"":                     ShapeUnknown,
		"random-junk":          ShapeUnknown,
		"has . two . spaces x": ShapeUnknown,
	}
	for raw, want := range tests {
		if got := Looks(raw); got != want {
			t.Errorf("Looks(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestRequireScope(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	tests := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"has scope", []string{ScopeSheetsRead}, http.StatusNoContent},
		{"lacks scope", []string{ScopeAPIKeysRead}, http.StatusForbidden},
		{"no scopes", nil, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			ctx := session.PrincipalInto(r.Context(), session.Principal{Scopes: tc.scopes})
			w := httptest.NewRecorder()
			RequireScope(ScopeSheetsRead)(next).ServeHTTP(w, r.WithContext(ctx))
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
