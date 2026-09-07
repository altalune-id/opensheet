package authl

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type pkce struct {
	state     string
	nonce     string
	verifier  string
	challenge string
}

func newPKCE() (*pkce, error) {
	state, err := randB64URL(24)
	if err != nil {
		return nil, err
	}
	nonce, err := randB64URL(24)
	if err != nil {
		return nil, err
	}
	verifier, err := randB64URL(48)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return &pkce{
		state:     state,
		nonce:     nonce,
		verifier:  verifier,
		challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

type statePayload struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Return   string `json:"r,omitempty"`
	Exp      int64  `json:"e"`
}

func (c *Client) encodeStateCookie(p *pkce, returnTo string) (string, error) {
	pl := statePayload{
		State:    p.state,
		Nonce:    p.nonce,
		Verifier: p.verifier,
		Return:   returnTo,
		Exp:      time.Now().Add(c.cfg.StateMaxAge).Unix(),
	}
	raw, err := json.Marshal(pl)
	if err != nil {
		return "", err
	}
	sig := hmac.New(sha256.New, c.cfg.StateSecret)
	sig.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(sig.Sum(nil)), nil
}

func (c *Client) decodeStateCookie(v string) (*statePayload, error) {
	parts := strings.Split(v, ".")
	if len(parts) != 2 {
		return nil, errors.New("authl: state cookie malformed")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("authl: state cookie payload not base64url")
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("authl: state cookie signature not base64url")
	}
	want := hmac.New(sha256.New, c.cfg.StateSecret)
	want.Write(raw)
	if subtle.ConstantTimeCompare(got, want.Sum(nil)) != 1 {
		return nil, errors.New("authl: state cookie signature mismatch")
	}
	var pl statePayload
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, errors.New("authl: state cookie payload not json")
	}
	if time.Now().Unix() >= pl.Exp {
		return nil, errors.New("authl: state cookie expired")
	}
	return &pl, nil
}

func (c *Client) setStateCookie(w http.ResponseWriter, r *http.Request, val string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: SameSite=Lax set below
		Name:     c.cfg.StateCookie,
		Value:    val,
		Path:     c.cfg.CookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.cfg.CookieSecure || r.TLS != nil,
		MaxAge:   int(c.cfg.StateMaxAge.Seconds()),
	})
}

func (c *Client) clearStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: SameSite=Lax set below
		Name:     c.cfg.StateCookie,
		Value:    "",
		Path:     c.cfg.CookiePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.cfg.CookieSecure || r.TLS != nil,
		MaxAge:   -1,
	})
}

// SECURITY: HttpOnly defaults on so the email isn't an XSS-exfil target.
func (c *Client) setLastUserCookie(w http.ResponseWriter, r *http.Request, email string) {
	if email == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: SameSite=Lax set below
		Name:     c.cfg.LastUserCookie,
		Value:    email,
		Path:     c.cfg.CookiePath,
		HttpOnly: !c.cfg.LastUserCookieJSReadable,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.cfg.CookieSecure || r.TLS != nil,
		MaxAge:   int(c.cfg.LastUserMaxAge.Seconds()),
	})
}

func (c *Client) clearLastUserCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: SameSite=Lax set below
		Name:     c.cfg.LastUserCookie,
		Value:    "",
		Path:     c.cfg.CookiePath,
		HttpOnly: !c.cfg.LastUserCookieJSReadable,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.cfg.CookieSecure || r.TLS != nil,
		MaxAge:   -1,
	})
}

func randB64URL(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
