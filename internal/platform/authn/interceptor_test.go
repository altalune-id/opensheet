package authn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

type emptyReq struct{}

func newRPC(header map[string]string) *connect.Request[emptyReq] {
	req := connect.NewRequest(&emptyReq{})
	for k, v := range header {
		req.Header().Set(k, v)
	}
	return req
}

type detailedAuth struct {
	err   error
	calls *int
}

func (d detailedAuth) Authenticate(context.Context, string) (session.Principal, error) {
	if d.calls != nil {
		*d.calls++
	}
	return session.Principal{}, d.err
}

func downstream(calls *int, got *session.Principal) connect.UnaryFunc {
	return func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		*calls++
		if got != nil {
			*got = session.PrincipalFrom(ctx)
		}
		return connect.NewResponse(&emptyReq{}), nil
	}
}

func TestInterceptor_HeaderFailuresAreUnauthenticated(t *testing.T) {
	cases := []struct {
		name    string
		header  map[string]string
		missing bool
		scheme  string
	}{
		{name: "no header", header: nil, missing: true},
		{name: "empty bearer", header: map[string]string{"Authorization": "Bearer "}, missing: true},
		{name: "empty api key", header: map[string]string{"X-API-Key": "  "}, missing: true},
		{name: "bare bearer", header: map[string]string{"Authorization": "Bearer"}, missing: true},
		{name: "basic scheme", header: map[string]string{"Authorization": "Basic YWJj"}, scheme: "Basic"},
		{name: "opaque scheme", header: map[string]string{"Authorization": "abcdef"}, scheme: "abcdef"},
		{name: "uppercase scheme", header: map[string]string{"Authorization": "BEARER abc"}, scheme: "BEARER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chainCalls, nextCalls := 0, 0
			inter := Interceptor(Chain{detailedAuth{calls: &chainCalls}})
			_, err := inter(downstream(&nextCalls, nil))(context.Background(), newRPC(tc.header))
			if err == nil {
				t.Fatal("expected an error")
			}
			var connErr *connect.Error
			if !errors.As(err, &connErr) || connErr.Code() != connect.CodeUnauthenticated {
				t.Fatalf("got %T: %v, want connect Unauthenticated", err, err)
			}
			if chainCalls != 0 {
				t.Errorf("chain called %d times, want 0", chainCalls)
			}
			if nextCalls != 0 {
				t.Errorf("downstream called %d times, want 0", nextCalls)
			}
			if tc.missing && !tokens.IsMissingAuthError(err) {
				t.Fatalf("got %T: %v, want *tokens.MissingAuthError", err, err)
			}
			if tc.scheme == "" {
				return
			}
			var bad *tokens.BadSchemeError
			if !errors.As(err, &bad) {
				t.Fatalf("got %T: %v, want *tokens.BadSchemeError", err, err)
			}
			if bad.Scheme != tc.scheme {
				t.Errorf("Scheme=%q want %q", bad.Scheme, tc.scheme)
			}
		})
	}
}

func TestInterceptor_InjectsPrincipal(t *testing.T) {
	want := session.Principal{
		UserID:      uuid.Must(uuid.NewV7()),
		Email:       "member@example.com",
		ActiveOrgID: uuid.Must(uuid.NewV7()),
		Scopes:      AllScopes(),
	}
	for name, header := range map[string]map[string]string{
		"bearer":         {"Authorization": "Bearer good"},
		"lowercase":      {"Authorization": "bearer good"},
		"x-api-key":      {"X-API-Key": "good"},
		"bearer padding": {"Authorization": "Bearer  good "},
	} {
		t.Run(name, func(t *testing.T) {
			var got session.Principal
			nextCalls := 0
			inter := Interceptor(Chain{stubAuth{accept: "good", p: want}})
			if _, err := inter(downstream(&nextCalls, &got))(context.Background(), newRPC(header)); err != nil {
				t.Fatalf("Interceptor: %v", err)
			}
			if nextCalls != 1 {
				t.Fatalf("downstream called %d times, want 1", nextCalls)
			}
			if got.UserID != want.UserID || got.ActiveOrgID != want.ActiveOrgID || got.Email != want.Email {
				t.Errorf("principal not injected: %+v", got)
			}
		})
	}
}

func TestInterceptor_ChainFailureStaysOpaque(t *testing.T) {
	const secret = "user 7f3 has no membership in org acme"
	nextCalls := 0
	inter := Interceptor(Chain{detailedAuth{err: errors.New(secret)}})
	_, err := inter(downstream(&nextCalls, nil))(context.Background(), newRPC(map[string]string{
		"Authorization": "Bearer good",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	if nextCalls != 0 {
		t.Errorf("downstream called %d times, want 0", nextCalls)
	}
	if !IsUnauthorizedError(err) {
		t.Fatalf("got %T: %v, want *UnauthorizedError", err, err)
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("got %T: %v, want connect Unauthenticated", err, err)
	}
	if strings.Contains(connErr.Message(), secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("authenticator detail leaked: %q", err.Error())
	}
	if !strings.Contains(connErr.Message(), (&UnauthorizedError{}).Error()) {
		t.Errorf("message=%q want the opaque authn error", connErr.Message())
	}
}

func TestInterceptor_EmptyChainRejects(t *testing.T) {
	nextCalls := 0
	inter := Interceptor(nil)
	_, err := inter(downstream(&nextCalls, nil))(context.Background(), newRPC(map[string]string{
		"Authorization": "Bearer good",
	}))
	if !IsUnauthorizedError(err) {
		t.Fatalf("got %T: %v, want *UnauthorizedError", err, err)
	}
	if nextCalls != 0 {
		t.Errorf("downstream called %d times, want 0", nextCalls)
	}
}

func TestCredentialFromHeader(t *testing.T) {
	cases := map[string]struct {
		header map[string]string
		want   string
	}{
		"bearer":            {header: map[string]string{"Authorization": "Bearer abc"}, want: "abc"},
		"lowercase bearer":  {header: map[string]string{"Authorization": "bearer abc"}, want: "abc"},
		"padded bearer":     {header: map[string]string{"Authorization": "Bearer  abc  "}, want: "abc"},
		"api key":           {header: map[string]string{"X-API-Key": " osk_abc "}, want: "osk_abc"},
		"foreign scheme":    {header: map[string]string{"Authorization": "Basic abc"}, want: ""},
		"authz wins":        {header: map[string]string{"Authorization": "Bearer abc", "X-API-Key": "osk_x"}, want: "abc"},
		"nothing":           {header: nil, want: ""},
		"scheme without tk": {header: map[string]string{"Authorization": "Bearer"}, want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := CredentialFromHeader(newRPC(tc.header).Header())
			if got != tc.want {
				t.Errorf("CredentialFromHeader=%q want %q", got, tc.want)
			}
		})
	}
}
