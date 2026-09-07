package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

// SessionCookieName is the browser-visible cookie carrying the signed session id.
const SessionCookieName = "altempl_sid"

// InviteCookieName carries a raw invite token across an unauth OIDC round-trip.
const InviteCookieName = "altempl_invite"

// SessionTTL is the maximum age of a session id cookie.
const SessionTTL = 12 * time.Hour

// ErrBadCookie is returned when a cookie fails HMAC verification.
var ErrBadCookie = errors.New("web: cookie signature invalid")

// NewSID mints a 24-byte random session id, base64-url encoded.
func NewSID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SignCookie returns "value|hmac" where hmac is base64-url-encoded HMAC-SHA256(value) keyed on secret.
func SignCookie(secret []byte, value string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(value))
	return value + "|" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCookie splits a signed cookie and constant-time-compares the HMAC.
func VerifyCookie(secret []byte, raw string) (string, error) {
	i := strings.LastIndexByte(raw, '|')
	if i <= 0 || i == len(raw)-1 {
		return "", ErrBadCookie
	}
	value := raw[:i]
	sig, err := base64.RawURLEncoding.DecodeString(raw[i+1:])
	if err != nil {
		return "", ErrBadCookie
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(value))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", ErrBadCookie
	}
	return value, nil
}

// CookieOpts collects what handlers need to write a well-formed cookie.
type CookieOpts struct {
	Name         string
	Value        string
	BasePath     string
	CookieSecure bool
	MaxAge       int
}

// SetCookie writes a signed cookie with the project's defaults (HttpOnly, SameSite=Lax).
func SetCookie(w http.ResponseWriter, o CookieOpts) {
	path := o.BasePath
	if path == "" {
		path = "/"
	} else {
		path = strings.TrimRight(path, "/") + "/"
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: SameSite=Lax set below
		Name:     o.Name,
		Value:    o.Value,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   o.CookieSecure,
		MaxAge:   o.MaxAge,
	})
}

// ClearCookie writes an expired cookie with the same posture as SetCookie.
func ClearCookie(w http.ResponseWriter, name, basePath string, secure bool) {
	SetCookie(w, CookieOpts{Name: name, Value: "", BasePath: basePath, CookieSecure: secure, MaxAge: -1})
}
