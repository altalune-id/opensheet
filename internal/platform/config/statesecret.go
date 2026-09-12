package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// StateSecretMinLen is the shortest decoding of http.stateSecret accepted as a cookie HMAC key.
const StateSecretMinLen = 32

const stateSecretHowTo = "generate one with `openssl rand -hex 32` (set OPENSHEET_HTTP_STATE_SECRET)"

// ParseStateSecret decodes http.stateSecret as hex, then base64url, then standard base64, and checks its length.
func ParseStateSecret(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, &StateSecretMissingError{}
	}
	buf, err := decodeStateSecret(s)
	if err != nil {
		return nil, &StateSecretEncodingError{Cause: err}
	}
	if len(buf) < StateSecretMinLen {
		return nil, &StateSecretTooShortError{Len: len(buf)}
	}
	return buf, nil
}

// NOTE: hex is tried first to match sealer.ParseKey, so one `openssl rand -hex 32` value keys both secrets.
func decodeStateSecret(s string) ([]byte, error) {
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// StateSecretMissingError reports an empty http.stateSecret.
type StateSecretMissingError struct{}

func (*StateSecretMissingError) Error() string {
	return "config: http.stateSecret is empty — " + stateSecretHowTo
}

// IsStateSecretMissingError reports whether err's chain contains a *StateSecretMissingError.
func IsStateSecretMissingError(err error) bool {
	_, ok := errors.AsType[*StateSecretMissingError](err)
	return ok
}

// StateSecretEncodingError reports an http.stateSecret that is neither hex nor base64.
type StateSecretEncodingError struct{ Cause error }

func (*StateSecretEncodingError) Error() string {
	return "config: http.stateSecret is neither hex nor base64 — " + stateSecretHowTo
}

func (e *StateSecretEncodingError) Unwrap() error { return e.Cause }

// IsStateSecretEncodingError reports whether err's chain contains a *StateSecretEncodingError.
func IsStateSecretEncodingError(err error) bool {
	_, ok := errors.AsType[*StateSecretEncodingError](err)
	return ok
}

// StateSecretTooShortError reports an http.stateSecret decoding to fewer than [StateSecretMinLen] bytes.
type StateSecretTooShortError struct{ Len int }

func (e *StateSecretTooShortError) Error() string {
	return fmt.Sprintf("config: http.stateSecret decodes to %d bytes, want at least %d — %s",
		e.Len, StateSecretMinLen, stateSecretHowTo)
}

// IsStateSecretTooShortError reports whether err's chain contains a *StateSecretTooShortError.
func IsStateSecretTooShortError(err error) bool {
	_, ok := errors.AsType[*StateSecretTooShortError](err)
	return ok
}
