// Package reqid propagates a request identifier across a single call via context.
package reqid

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// Header is the canonical wire name across HTTP, Connect, and future gRPC.
const Header = "X-Request-Id"

type ctxKey struct{}

// New mints a fresh time-ordered UUIDv7 as a 36-char string.
func New() string { return uuid.Must(uuid.NewV7()).String() }

// FromContext returns the request ID in ctx, or "" if absent.
func FromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// WithContext attaches id to ctx.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// Ensure returns ctx unchanged if it already carries an ID; otherwise wraps it with a fresh one.
func Ensure(ctx context.Context) (_ context.Context, id string) {
	if id = FromContext(ctx); id != "" {
		return ctx, id
	}
	id = New()
	return WithContext(ctx, id), id
}

// MaxLength is the longest inbound request id that is propagated rather than replaced.
const MaxLength = 128

// Sanitize returns id when it is safe to propagate as a correlation id, and "" otherwise. SECURITY: an attacker controls this on unauthenticated surfaces and it lands in every correlated log line, so it is capped and held to the UUID/traceparent charset: https://www.w3.org/TR/trace-context/
func Sanitize(id string) string {
	if id == "" || len(id) > MaxLength {
		return ""
	}
	for i := range len(id) {
		if !safeByte(id[i]) {
			return ""
		}
	}
	return id
}

func safeByte(c byte) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return true
	case c == '-', c == '_', c == '.', c == ':':
		return true
	}
	return false
}

// FromHeader returns the sanitized X-Request-Id carried by h, or "" when it is absent or unsafe.
func FromHeader(h http.Header) string { return Sanitize(h.Get(Header)) }

// FromHTTPHeader reads a sanitized X-Request-Id from an incoming HTTP request.
func FromHTTPHeader(r *http.Request) string { return FromHeader(r.Header) }
