package authn

import (
	"context"
	"slices"

	"connectrpc.com/connect"

	"altalune.id/opensheet/internal/platform/session"
)

// ScopeTable maps a Connect procedure to the scope a key or token principal must hold to call it, or to SessionOnly.
type ScopeTable map[string]string

// SessionOnly marks a procedure in a ScopeTable that only a signed-in person may call; no key or token is ever admitted.
const SessionOnly = "session-only"

// Interceptor authenticates the credential and enforces the scope table on every credential but a session. SECURITY: a procedure absent from the table is denied, so a new RPC fails closed.
func Interceptor(c Chain, table ScopeTable) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			raw := CredentialFromHeader(req.Header())
			if raw == "" {
				return nil, &UnauthorizedError{}
			}
			p, err := c.Authenticate(ctx, raw)
			if err != nil {
				return nil, err
			}
			if !isSession(p.Source) {
				want, ok := table[req.Spec().Procedure]
				if !ok {
					return nil, &InsufficientScopeError{Scope: "undeclared"}
				}
				if want == SessionOnly {
					return nil, &SessionRequiredError{}
				}
				if !slices.Contains(p.Scopes, want) {
					return nil, &InsufficientScopeError{Scope: want}
				}
			}
			return next(session.PrincipalInto(ctx, p), req)
		}
	}
}

func isSession(src session.Source) bool {
	switch src {
	case session.SourceGenesis, session.SourceOIDC, session.SourceLocal:
		return true
	}
	return false
}
