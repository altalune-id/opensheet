package tokens

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	"altalune.id/template/internal/platform/session"
)

// Interceptor extracts Authorization: Bearer <jwt>, verifies it, and injects the Principal into ctx.
func Interceptor(v Verifier) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			h := req.Header().Get("Authorization")
			if h == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, &MissingAuthError{})
			}
			if !strings.HasPrefix(h, "Bearer ") {
				scheme := h
				if i := strings.IndexByte(h, ' '); i >= 0 {
					scheme = h[:i]
				}
				return nil, connect.NewError(connect.CodeUnauthenticated, &BadSchemeError{Scheme: scheme})
			}
			raw := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			p, err := v.Verify(ctx, raw)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}
			return next(session.PrincipalInto(ctx, p), req)
		}
	}
}
