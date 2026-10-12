// Package middleware bundles the outer-most HTTP middlewares for the SSR web server.
package middleware

import (
	"net/http"

	"altalune.id/opensheet/reqid"
)

// RequestID ensures every request carries an X-Request-Id, echoed on the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NOTE: mint unconditionally rather than reqid.Ensure — the listener's BaseContext
		// carries the process-wide id, which Ensure would reuse for every request.
		id := reqid.FromHTTPHeader(r)
		if id == "" {
			id = reqid.New()
		}
		ctx := reqid.WithContext(r.Context(), id)
		w.Header().Set(reqid.Header, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
