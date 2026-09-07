package authl

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
)

// LoopbackOption tweaks [Client.RunLoopback].
type LoopbackOption func(*loopbackConfig)

type loopbackConfig struct {
	host        string
	port        int
	openBrowser bool
	callbackTTL time.Duration
}

// WithLoopbackHost overrides the default 127.0.0.1 bind host; loopback per RFC 8252 Section 8.3 means 127.0.0.1 or [::1] — never "localhost".
func WithLoopbackHost(host string) LoopbackOption { return func(o *loopbackConfig) { o.host = host } }

// WithLoopbackPort pins the ephemeral port; default 0 picks a free port at runtime.
func WithLoopbackPort(port int) LoopbackOption { return func(o *loopbackConfig) { o.port = port } }

// WithOpenBrowser controls whether RunLoopback opens the authorize URL in the OS browser.
func WithOpenBrowser(open bool) LoopbackOption {
	return func(o *loopbackConfig) { o.openBrowser = open }
}

// WithCallbackTTL sets how long RunLoopback waits for the browser callback; default 5 minutes.
func WithCallbackTTL(d time.Duration) LoopbackOption {
	return func(o *loopbackConfig) { o.callbackTTL = d }
}

// RunLoopback binds a loopback port, opens the authorize URL, waits for the callback, and exchanges the code for an Identity.
func (c *Client) RunLoopback(ctx context.Context, opts ...LoopbackOption) (*Identity, error) {
	o := loopbackConfig{host: "127.0.0.1", openBrowser: true, callbackTTL: 5 * time.Minute}
	for _, opt := range opts {
		opt(&o)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", o.host, o.port))
	if err != nil {
		return nil, fmt.Errorf("authl: bind loopback: %w", err)
	}
	defer func() { _ = listener.Close() }()

	redirectURI := fmt.Sprintf("http://%s/callback", listener.Addr().String())

	p, err := newPKCE()
	if err != nil {
		return nil, err
	}
	oauthCfg := *c.oauth
	oauthCfg.RedirectURL = redirectURI
	authURL := c.authorizeURLFromCfg(&oauthCfg, p, "", "")

	fmt.Println("Open this URL in your browser to sign in:")
	fmt.Println("  " + authURL)
	if o.openBrowser {
		_ = openBrowser(authURL)
	}

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("state"); subtle.ConstantTimeCompare([]byte(got), []byte(p.state)) != 1 {
			writeLoopbackHTML(w, false, "state mismatch")
			resCh <- result{err: errors.New("authl: loopback state mismatch")}
			return
		}
		if c.issParameterSupported {
			if got := q.Get("iss"); got != c.cfg.Issuer {
				writeLoopbackHTML(w, false, "iss mismatch")
				resCh <- result{err: fmt.Errorf("authl: loopback iss %q does not match issuer %q", got, c.cfg.Issuer)}
				return
			}
		}
		if oe := q.Get("error"); oe != "" {
			desc := q.Get("error_description")
			writeLoopbackHTML(w, false, fmt.Sprintf("%s: %s", oe, desc))
			resCh <- result{err: fmt.Errorf("authl: authorize returned %s: %s", oe, desc)}
			return
		}
		code := q.Get("code")
		if code == "" {
			writeLoopbackHTML(w, false, "no code")
			resCh <- result{err: errors.New("authl: loopback callback missing code")}
			return
		}
		writeLoopbackHTML(w, true, "")
		resCh <- result{code: code}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		sdCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(sdCtx)
	}()

	waitCtx, cancel := context.WithTimeout(ctx, o.callbackTTL)
	defer cancel()

	var code string
	select {
	case res := <-resCh:
		if res.err != nil {
			return nil, res.err
		}
		code = res.code
	case <-waitCtx.Done():
		return nil, fmt.Errorf("authl: timed out waiting for browser callback: %w", waitCtx.Err())
	}
	return c.exchangeWith(ctx, &oauthCfg, code, p.verifier, p.nonce)
}

func (c *Client) authorizeURLFromCfg(cfg *oauth2.Config, p *pkce, prompt, loginHint string) string {
	opts := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", p.challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("nonce", p.nonce),
	}
	if c.cfg.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.cfg.Resource))
	}
	if prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return cfg.AuthCodeURL(p.state, opts...)
}

func (c *Client) exchangeWith(ctx context.Context, cfg *oauth2.Config, code, verifier, nonce string) (*Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.cfg.HTTPClient)
	opts := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("code_verifier", verifier)}
	if c.cfg.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.cfg.Resource))
	}
	token, err := cfg.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("authl: loopback exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("authl: loopback id_token missing")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("authl: loopback verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, errors.New("authl: loopback id_token nonce mismatch")
	}
	if err := verifyAzp(idToken, c.cfg.ClientID); err != nil {
		return nil, err
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("authl: loopback extract claims: %w", err)
	}
	if claims.Email == "" || claims.Name == "" {
		if uc, err := c.fetchUserInfo(ctx, token); err == nil {
			if claims.Email == "" {
				claims.Email = uc.Email
			}
			if claims.Name == "" {
				claims.Name = uc.Name
			}
		}
	}
	return &Identity{
		Subject:      idToken.Subject,
		Email:        claims.Email,
		Name:         claims.Name,
		IDToken:      rawIDToken,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry.Unix(),
	}, nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // G204: URL is a validated OIDC authorization URL from the trusted issuer discovery
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // G204: URL is a validated OIDC authorization URL from the trusted issuer discovery
	default:
		cmd = exec.Command("xdg-open", url) //nolint:gosec // G204: URL is a validated OIDC authorization URL from the trusted issuer discovery
	}
	return cmd.Start()
}

func writeLoopbackHTML(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Signed in</title>
<style>body{font:16px system-ui;padding:2rem;color:#111}h1{font-weight:600}</style>
<h1>You're signed in.</h1><p>You can close this tab and return to your terminal.</p>`))
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Sign-in failed</title>
<style>body{font:16px system-ui;padding:2rem;color:#111}h1{font-weight:600}code{background:#eee;padding:.15em .3em;border-radius:3px}</style>
<h1>Sign-in failed.</h1><p>Reason: <code>` + html.EscapeString(msg) + `</code></p>`))
}
