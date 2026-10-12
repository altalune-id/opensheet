package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

type nonceCtxKey struct{}

// CSPOptions configures the Content-Security-Policy the middleware emits.
type CSPOptions struct {
	// Enabled turns the header on; a disabled middleware still mints a nonce.
	Enabled bool
	// ReportOnly sends Content-Security-Policy-Report-Only, which logs violations without blocking.
	ReportOnly bool
	// ReportURI receives violation reports. Empty omits the directive.
	ReportURI string
	// ExtraScriptSrc and ExtraStyleSrc append origins a fork's assets need, such as a CDN.
	ExtraScriptSrc []string
	ExtraStyleSrc  []string
	// ExtraConnectSrc appends origins XHR/fetch/SSE may reach.
	ExtraConnectSrc []string
}

// CSP sets a nonce-based Content-Security-Policy and exposes the nonce on the request context. SECURITY: script-src carries a nonce rather than 'unsafe-inline'; style-src keeps 'unsafe-inline' because the layout sets style="" attributes a nonce cannot cover.
func CSP(opts CSPOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := newNonce()
			ctx := context.WithValue(r.Context(), nonceCtxKey{}, nonce)
			if opts.Enabled {
				header := "Content-Security-Policy"
				if opts.ReportOnly {
					header = "Content-Security-Policy-Report-Only"
				}
				w.Header().Set(header, buildPolicy(nonce, opts))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// NonceFrom returns the per-request CSP nonce, or "" when no CSP middleware ran.
func NonceFrom(ctx context.Context) string {
	n, _ := ctx.Value(nonceCtxKey{}).(string)
	return n
}

func buildPolicy(nonce string, opts CSPOptions) string {
	scriptSrc := append([]string{"'self'", "'nonce-" + nonce + "'"}, opts.ExtraScriptSrc...)
	styleSrc := append([]string{"'self'", "'unsafe-inline'"}, opts.ExtraStyleSrc...)
	connectSrc := append([]string{"'self'"}, opts.ExtraConnectSrc...)

	directives := []string{
		"default-src 'self'",
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src " + strings.Join(styleSrc, " "),
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connectSrc, " "),
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}
	if opts.ReportURI != "" {
		directives = append(directives, "report-uri "+opts.ReportURI)
	}
	return strings.Join(directives, "; ")
}

func newNonce() string {
	b := make([]byte, 16)
	// NOTE: crypto/rand.Read never returns an error since Go 1.24; it panics on a broken source.
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}
