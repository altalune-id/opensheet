package middleware

import (
	"fmt"
	"net/http"

	"altalune.id/opensheet/internal/apperror"
)

// SECURITY: a machine surface must never fall back to the SSR error page, and the body carries no detail beyond "internal".
const recoverJSONBody = `{"code":"internal","message":"internal"}`

// RecoverJSON installs a panic handler for the machine surfaces, answering with their JSON error envelope.
func RecoverJSON(reporter apperror.UnexpectedFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				_ = reporter(r.Context(), "web.panic", fmt.Errorf("panic: %v", rec), "path", r.URL.Path)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(recoverJSONBody))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
