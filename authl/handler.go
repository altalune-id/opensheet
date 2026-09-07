package authl

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// StartHandler is the outbound-leg handler; honors ?switch=1 (force account picker) and ?return_to=/path.
func (c *Client) StartHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := newPKCE()
		if err != nil {
			c.writeErr(w, r, http.StatusInternalServerError, "sign-in start", err)
			return
		}
		returnTo := sanitizeReturnTo(r.URL.Query().Get("return_to"))
		cookie, err := c.encodeStateCookie(p, returnTo)
		if err != nil {
			c.writeErr(w, r, http.StatusInternalServerError, "encode state", err)
			return
		}
		c.setStateCookie(w, r, cookie)

		var prompt, hint string
		switch {
		case r.URL.Query().Get("switch") == "1":
			prompt = "select_account"
		default:
			if c.cfg.RememberLastUser {
				hint = c.LastKnownUser(r)
			}
			if hint == "" {
				// SECURITY: unknown intended user — force the account picker to avoid silent SSO onto whichever OP session is live.
				prompt = "select_account"
			}
		}

		http.Redirect(w, r, c.authorizeURL(p, prompt, hint), http.StatusFound)
	})
}

// CallbackHandler is the inbound-leg handler; onComplete receives the verified Identity.
func (c *Client) CallbackHandler(onComplete OnComplete) http.Handler {
	if onComplete == nil {
		panic("authl: CallbackHandler called with nil OnComplete")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := readCookie(r, c.cfg.StateCookie)
		if raw == "" {
			c.writeErr(w, r, http.StatusBadRequest, "no pending sign-in", errors.New("state cookie missing"))
			return
		}
		pl, err := c.decodeStateCookie(raw)
		if err != nil {
			c.clearStateCookie(w, r)
			c.writeErr(w, r, http.StatusBadRequest, "sign-in expired", err)
			return
		}

		q := r.URL.Query()
		if got := q.Get("state"); subtle.ConstantTimeCompare([]byte(got), []byte(pl.State)) != 1 {
			c.clearStateCookie(w, r)
			c.writeErr(w, r, http.StatusBadRequest, "state mismatch",
				errors.New("callback state does not match cookie"))
			return
		}
		if c.issParameterSupported {
			if got := q.Get("iss"); got != c.cfg.Issuer {
				c.clearStateCookie(w, r)
				c.writeErr(w, r, http.StatusBadRequest, "iss mismatch",
					fmt.Errorf("callback iss %q does not match issuer %q", got, c.cfg.Issuer))
				return
			}
		}
		if oe := q.Get("error"); oe != "" {
			c.clearStateCookie(w, r)
			c.writeErr(w, r, http.StatusBadRequest, "provider rejected sign-in",
				fmt.Errorf("%s: %s", oe, q.Get("error_description")))
			return
		}
		code := q.Get("code")
		if code == "" {
			c.clearStateCookie(w, r)
			c.writeErr(w, r, http.StatusBadRequest, "missing code",
				errors.New("callback did not carry code"))
			return
		}

		ident, err := c.exchange(r.Context(), code, pl.Verifier, pl.Nonce)
		if err != nil {
			c.clearStateCookie(w, r)
			c.writeErr(w, r, http.StatusBadGateway, "token exchange failed", err)
			return
		}

		c.clearStateCookie(w, r)
		if c.cfg.RememberLastUser {
			c.setLastUserCookie(w, r, ident.Email)
		}

		if err := onComplete(r.Context(), w, r, ident); err != nil {
			c.writeErr(w, r, http.StatusInternalServerError, "on-complete", err)
			return
		}
		if !responseWritten(w) && pl.Return != "" {
			http.Redirect(w, r, pl.Return, http.StatusSeeOther)
		}
	})
}

// LogoutHandler redirects to ?return_to; use Client.EndSessionURL for RP-Initiated Logout.
func (c *Client) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		to := sanitizeReturnTo(r.URL.Query().Get("return_to"))
		if to == "" {
			to = "/"
		}
		http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // G710: to sanitized via sanitizeReturnTo (same-origin absolute path only)
	})
}

func (c *Client) authorizeURL(p *pkce, prompt, loginHint string) string {
	opts := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", p.challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("nonce", p.nonce),
	}
	if c.cfg.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.cfg.Resource))
	}
	if strings.TrimSpace(prompt) != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	if strings.TrimSpace(loginHint) != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return c.oauth.AuthCodeURL(p.state, opts...)
}

func (c *Client) exchange(ctx context.Context, code, verifier, nonce string) (*Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.cfg.HTTPClient)

	opts := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("code_verifier", verifier)}
	if c.cfg.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", c.cfg.Resource))
	}
	token, err := c.oauth.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("authl: exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("authl: id_token missing from token response")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("authl: verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, errors.New("authl: id_token nonce mismatch")
	}
	if err := verifyAzp(idToken, c.cfg.ClientID); err != nil {
		return nil, err
	}

	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("authl: extract claims: %w", err)
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

type userInfoClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

func (c *Client) fetchUserInfo(ctx context.Context, token *oauth2.Token) (*userInfoClaims, error) {
	src := c.oauth.TokenSource(ctx, token)
	ui, err := c.provider.UserInfo(ctx, src)
	if err != nil {
		return nil, err
	}
	var out userInfoClaims
	if err := ui.Claims(&out); err != nil {
		return nil, err
	}
	if out.Email == "" && ui.Email != "" {
		out.Email = ui.Email
	}
	return &out, nil
}

// sanitizeReturnTo accepts only same-origin absolute paths; rejects protocol-relative and absolute URLs, and normalizes backslashes to forward slashes.
func sanitizeReturnTo(raw string) string {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	if u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	if strings.HasPrefix(u.Path, "//") {
		return ""
	}
	out := u.EscapedPath()
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}

func responseWritten(w http.ResponseWriter) bool {
	return w.Header().Get("Content-Type") != "" || w.Header().Get("Location") != ""
}

// SECURITY: logs the original error server-side and writes a redacted body so upstream error text never leaks.
func (c *Client) writeErr(w http.ResponseWriter, _ *http.Request, status int, title string, err error) {
	log.Printf("authl: %s: %v", title, err)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, "Sign-in failed. See server logs for details.")
}
