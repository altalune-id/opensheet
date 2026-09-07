// Package auth orchestrates local + OIDC login and returns a session.Principal.
package auth

import (
	"altalune.id/template/internal/platform/session"
)

// Credentials is the local-login request shape.
type Credentials struct {
	Email    string
	Password string
}

// OIDCClaims is the third-party identity claim set consumed by LoginOIDC.
type OIDCClaims struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
}

// Principal is the session identity returned by login workflows.
type Principal = session.Principal
