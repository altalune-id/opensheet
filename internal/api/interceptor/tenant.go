package interceptor

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
)

// Tenant derives a tenant.Context from the Principal in ctx and threads it forward.
func Tenant() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			p := session.PrincipalFrom(ctx)
			if p.UserID == uuid.Nil && p.ActiveOrgID == uuid.Nil && p.ActiveProjectID == uuid.Nil {
				return next(ctx, req)
			}
			ctx = tenant.Into(ctx, tenant.Context{
				OrgID:     p.ActiveOrgID,
				ProjectID: p.ActiveProjectID,
				UserID:    p.UserID,
			})
			return next(ctx, req)
		}
	}
}
