package middleware

import (
	"net/http"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/web"
)

// SessionConfig bundles the pieces Session needs to verify a session cookie.
type SessionConfig struct {
	Store  session.Store
	Secret []byte
}

// Session reads the sid cookie, verifies its HMAC, and threads the Principal into ctx.
func Session(cfg SessionConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(web.SessionCookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			sid, err := web.VerifyCookie(cfg.Secret, c.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			p, ok, err := cfg.Store.Load(r.Context(), sid)
			if err != nil || !ok {
				next.ServeHTTP(w, r)
				return
			}
			ctx := session.PrincipalInto(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
