package authn

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

// Interceptor authenticates every unary RPC through c and injects the Principal into ctx.
func Interceptor(c Chain) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			raw, err := requireCredential(req.Header())
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}
			p, err := c.Authenticate(ctx, raw)
			if err != nil {
				// SECURITY: the chain's opaque error is the only detail that reaches the wire.
				return nil, connect.NewError(connect.CodeUnauthenticated, &UnauthorizedError{})
			}
			return next(session.PrincipalInto(ctx, p), req)
		}
	}
}

func requireCredential(h http.Header) (string, error) {
	if raw := CredentialFromHeader(h); raw != "" {
		return raw, nil
	}
	authz := h.Get("Authorization")
	if authz == "" {
		return "", &tokens.MissingAuthError{}
	}
	scheme, _, _ := strings.Cut(authz, " ")
	if scheme == "Bearer" || scheme == "bearer" {
		return "", &tokens.MissingAuthError{}
	}
	return "", &tokens.BadSchemeError{Scheme: scheme}
}
