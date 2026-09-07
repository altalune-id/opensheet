package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
)

// Tenant scopes the request context to the signed-in principal's active org.
func Tenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NOTE: without this every handler must remember tenant.Into; the ones that forgot failed with tenant: missing context.
		p := session.PrincipalFrom(r.Context())
		if p.ActiveOrgID == uuid.Nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := tenant.Into(r.Context(), tenant.Context{
			OrgID:     p.ActiveOrgID,
			UserID:    p.UserID,
			ProjectID: p.ActiveProjectID,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
