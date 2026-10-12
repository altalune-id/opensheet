package authn

import (
	"net/http"
	"slices"

	"altalune.id/opensheet/internal/platform/session"
)

// RequireScope rejects a request whose principal lacks scope.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !slices.Contains(session.PrincipalFrom(r.Context()).Scopes, scope) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
