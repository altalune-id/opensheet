// Package middleware bundles the outer-most HTTP middlewares for the SSR web server.
package middleware

import (
	"net/http"

	"altalune.id/template/reqid"
)

// RequestID ensures every request carries an X-Request-Id, echoed on the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if inbound := reqid.FromHTTPHeader(r); inbound != "" {
			ctx = reqid.WithContext(ctx, inbound)
		}
		ctx, id := reqid.Ensure(ctx)
		w.Header().Set(reqid.Header, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
