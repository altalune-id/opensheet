package middleware

import (
	"fmt"
	"net/http"

	"altalune.id/template/internal/apperror"
)

// Recover installs a panic handler that reports via reporter and renders via tmpl.
func Recover(reporter apperror.UnexpectedFunc, tmpl ErrorTemplate) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				err := fmt.Errorf("panic: %v", rec)
				appErr := reporter(r.Context(), "web.panic", err, "path", r.URL.Path)
				if tmpl != nil {
					_ = tmpl.RenderError(w, r, appErr)
					return
				}
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
