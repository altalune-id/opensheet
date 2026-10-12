package authn

import (
	"context"
	"slices"

	"connectrpc.com/connect"

	"altalune.id/opensheet/internal/platform/session"
)

// ScopeTable maps a Connect procedure to the scope a key principal must hold to call it.
type ScopeTable map[string]string

// Interceptor authenticates the credential and, for key principals, enforces the scope table. SECURITY: a procedure absent from the table is denied to key principals, so a new RPC fails closed.
func Interceptor(c Chain, scheme Scheme, table ScopeTable) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			raw := CredentialFromHeader(req.Header())
			if raw == "" {
				return nil, &UnauthorizedError{}
			}
			if scheme.Looks(raw) == ShapeUnknown {
				return nil, &UnauthorizedError{}
			}
			p, err := c.Authenticate(ctx, raw)
			if err != nil {
				return nil, err
			}
			if p.Source == session.SourceAPIKey {
				want, ok := table[req.Spec().Procedure]
				if !ok {
					return nil, &InsufficientScopeError{Scope: "undeclared"}
				}
				if !slices.Contains(p.Scopes, want) {
					return nil, &InsufficientScopeError{Scope: want}
				}
			}
			return next(session.PrincipalInto(ctx, p), req)
		}
	}
}
