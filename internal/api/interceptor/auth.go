package interceptor

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tokens"
)

// Auth verifies the Authorization header and injects the Principal into ctx.
func Auth(v tokens.Verifier) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			h := req.Header().Get("Authorization")
			if h == "" {
				return nil, &tokens.MissingAuthError{}
			}
			if !strings.HasPrefix(h, "Bearer ") {
				scheme := h
				if i := strings.IndexByte(h, ' '); i >= 0 {
					scheme = h[:i]
				}
				return nil, &tokens.BadSchemeError{Scheme: scheme}
			}
			raw := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			p, err := v.Verify(ctx, raw)
			if err != nil {
				return nil, err
			}
			return next(session.PrincipalInto(ctx, p), req)
		}
	}
}
