package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// ErrBadCookie is returned when a cookie fails HMAC verification.
var ErrBadCookie = errors.New("session: cookie signature invalid")

// Sign returns "value|hmac" where hmac is base64-url-encoded HMAC-SHA256(value) keyed on secret.
func Sign(secret []byte, value string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(value))
	return value + "|" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify splits a signed cookie and constant-time-compares the HMAC. Returns the raw value on success.
func Verify(secret []byte, raw string) (string, error) {
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
