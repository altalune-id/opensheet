package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	authv1 "altalune.id/template/gen/go/auth/v1"
	"altalune.id/template/internal/api"
)

func TestNewClient_SetsAuthHeaderOnCall(t *testing.T) {
	var seen string
	ts := httptest.NewServer(headerRecorder(&seen))
	t.Cleanup(ts.Close)

	c := api.NewClient(ts.URL, "the-token")
	_, _ = c.Auth.Whoami(t.Context(), connect.NewRequest(&authv1.WhoamiRequest{}))

	if seen != "Bearer the-token" {
		t.Errorf("Authorization = %q, want %q", seen, "Bearer the-token")
	}
}

func TestNewClient_EmptyToken_SkipsAuthHeader(t *testing.T) {
	var seen string
	ts := httptest.NewServer(headerRecorder(&seen))
	t.Cleanup(ts.Close)

	c := api.NewClient(ts.URL, "")
	_, _ = c.Auth.Whoami(t.Context(), connect.NewRequest(&authv1.WhoamiRequest{}))

	if seen != "" {
		t.Errorf("Authorization = %q, want empty", seen)
	}
}

func headerRecorder(dst *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*dst = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNotFound)
	})
}
