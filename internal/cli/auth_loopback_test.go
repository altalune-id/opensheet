package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/altalune-id/oidcrp/oidcrptest"

	"altalune.id/opensheet/internal/platform/config"
)

type tokenAuthRecorder struct {
	next  http.RoundTripper
	basic atomic.Bool
}

func (r *tokenAuthRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/token") {
		if _, _, ok := req.BasicAuth(); ok {
			r.basic.Store(true)
		}
	}
	return r.next.RoundTrip(req)
}

func TestLoopbackSignIn_UsesTheNativeClientOnALoopbackRedirect(t *testing.T) {
	idp := oidcrptest.New(t)
	rec := &tokenAuthRecorder{next: idp.Client().Transport}
	cfg := config.OIDCConfig{
		Issuer: idp.Issuer(), ClientID: "web-client", ClientSecret: "web-secret", CLIClientID: "cli-client",
		Scopes: []string{"openid", "email", "profile"},
	}
	rp := cliRelyingParty(cfg, &http.Client{Transport: rec})

	var out bytes.Buffer
	var seen url.Values
	open := func(ctx context.Context, authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		seen = u.Query()
		redirect, err := url.Parse(seen.Get("redirect_uri"))
		if err != nil {
			return err
		}
		redirect.RawQuery = idp.Authorize(authURL).Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, redirect.String(), http.NoBody)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return errors.New("no browser in tests")
	}

	tok, err := loopbackSignIn(t.Context(), rp, 0, &out, open)
	if err != nil {
		t.Fatalf("loopbackSignIn: %v", err)
	}
	if tok.Subject != idp.User.Subject || tok.Email != idp.User.Email || !tok.EmailVerified {
		t.Fatalf("token = %+v", tok)
	}
	if got := seen.Get("client_id"); got != "cli-client" {
		t.Fatalf("client_id = %q, want the native CLI client", got)
	}
	if got := seen.Get("redirect_uri"); !strings.HasPrefix(got, "http://127.0.0.1:") || !strings.HasSuffix(got, "/callback") {
		t.Fatalf("redirect_uri = %q, want a loopback IP with a bound port", got)
	}
	if rec.basic.Load() {
		t.Fatal("the CLI sent client credentials; a native client never holds the web client's secret")
	}
	if !strings.Contains(out.String(), seen.Get("state")) || !strings.Contains(out.String(), "open the URL above") {
		t.Fatalf("the URL must be printed, with a fallback when no browser opens; out=%q", out.String())
	}
}

func TestLoopbackSignIn_StopsWhenTheContextEnds(t *testing.T) {
	idp := oidcrptest.New(t)
	rp := cliRelyingParty(config.OIDCConfig{Issuer: idp.Issuer(), CLIClientID: "cli-client", Scopes: []string{"openid"}}, idp.Client())
	ctx, cancel := context.WithCancel(t.Context())
	open := func(context.Context, string) error {
		cancel()
		return nil
	}
	if _, err := loopbackSignIn(ctx, rp, 0, &bytes.Buffer{}, open); err == nil {
		t.Fatal("a cancelled sign-in must return an error, not hang")
	}
}
