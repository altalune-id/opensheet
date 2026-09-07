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

// FromHTTPHeader reads X-Request-Id from an incoming HTTP request.
func FromHTTPHeader(r *http.Request) string { return r.Header.Get(Header) }
