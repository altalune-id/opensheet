// Package authn authenticates a raw credential into a session.Principal, independent of credential kind.
package authn

import (
	"context"
	"net/http"
	"strings"

	"altalune.id/opensheet/internal/platform/session"
)

// KeyPrefix marks an opensheet API key.
const KeyPrefix = "osk_"

// Shape classifies a raw credential by form alone.
type Shape int

const (
	ShapeUnknown Shape = iota
	ShapeAPIKey
	ShapeJWT
)

// Looks classifies raw by shape alone.
func Looks(raw string) Shape {
	if raw == "" {
		return ShapeUnknown
	}
	if strings.HasPrefix(raw, KeyPrefix) {
		return ShapeAPIKey
	}
	if strings.Count(raw, ".") == 2 && !strings.Contains(raw, " ") {
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

// Authenticate implements Authenticator.
// SECURITY: every failure collapses to one opaque error.
func (c Chain) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	for _, a := range c {
		p, err := a.Authenticate(ctx, raw)
		if err == nil {
			return p, nil
		}
	}
	return session.Principal{}, &UnauthorizedError{}
}

// CredentialFrom extracts a raw credential from the Authorization or X-API-Key header.
// SECURITY: query strings are not consulted — they leak into access logs and Referer.
func CredentialFrom(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		for _, scheme := range []string{"Bearer ", "bearer "} {
			if raw, ok := strings.CutPrefix(h, scheme); ok {
				return strings.TrimSpace(raw)
			}
		}
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}
