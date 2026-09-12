package authn

import (
	"net/http"
	"slices"

	"altalune.id/opensheet/internal/platform/session"
)

// RequireScope rejects a request whose Principal lacks scope.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := session.PrincipalFrom(r.Context())
			if !slices.Contains(p.Scopes, scope) {
				// TODO: emit the JSON error envelope once apperror.HTTPStatus lands.
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
