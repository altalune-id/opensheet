// Package authn classifies and verifies machine credentials for the control and data planes.
package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"altalune.id/opensheet/internal/platform/session"
)

// Shape classifies a raw credential by form alone.
type Shape int

// The credential shapes Scheme can recognize.
const (
	ShapeUnknown Shape = iota
	ShapeAPIKey
	ShapeJWT
)

// Scheme classifies raw credentials; its zero value recognizes JWTs only.
type Scheme struct{ Prefix string }

// Looks classifies raw by shape alone, without touching a store.
func (s Scheme) Looks(raw string) Shape {
	if raw == "" {
		return ShapeUnknown
	}
	if s.Prefix != "" && strings.HasPrefix(raw, s.Prefix) {
		return ShapeAPIKey
	}
	if strings.Count(raw, ".") == 2 && !strings.ContainsAny(raw, " \t") {
		return ShapeJWT
	}
	return ShapeUnknown
}

// Authenticator turns a raw credential into a Principal.
type Authenticator interface {
	Authenticate(ctx context.Context, raw string) (session.Principal, error)
}

// Chain tries each Authenticator in order and returns the first success.
type Chain []Authenticator

// Authenticate implements Authenticator. SECURITY: every failure collapses to one opaque error, whatever the cause.
func (c Chain) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	var causes []error
	for _, a := range c {
		p, err := a.Authenticate(ctx, raw)
		if err == nil {
			return p, nil
		}
		causes = append(causes, err)
	}
	return session.Principal{}, &UnauthorizedError{causes: errors.Join(causes...)}
}

// CredentialFrom extracts a raw credential from the request's Authorization or X-API-Key header.
func CredentialFrom(r *http.Request) string { return CredentialFromHeader(r.Header) }

// CredentialFromHeader extracts a raw credential from an Authorization or X-API-Key header.
func CredentialFromHeader(h http.Header) string {
	if v := h.Get("Authorization"); v != "" {
		for _, scheme := range []string{"Bearer ", "bearer "} {
			if after, ok := strings.CutPrefix(v, scheme); ok {
				return strings.TrimSpace(after)
			}
		}
		return ""
	}
	return strings.TrimSpace(h.Get("X-API-Key"))
}
