package httpclient

import (
	"cmp"
	"time"
)

// Option configures a client built by New or NewProber.
type Option func(*settings)

type settings struct {
	allowPrivateHosts bool
	dialTimeout       time.Duration
	timeout           time.Duration
	responseBodyLimit int64
	otel              bool
	noTimeout         bool
	retry             RetryPolicy
}

// WithAllowPrivateHosts turns the SSRF dial filter off for trusted, operator-configured URLs.
func WithAllowPrivateHosts(allow bool) Option {
	return func(s *settings) { s.allowPrivateHosts = allow }
}

// WithDialTimeout bounds each dial attempt.
func WithDialTimeout(d time.Duration) Option {
	return func(s *settings) { s.dialTimeout = d }
}

// WithTimeout bounds the whole request, including redirects, body reads, and every retry attempt plus the backoff between them; zero keeps DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(s *settings) {
		s.timeout = cmp.Or(d, DefaultTimeout)
		s.noTimeout = false
	}
}

// WithoutTimeout removes the overall request bound; the transport still caps dial, TLS and response-header waits.
func WithoutTimeout() Option {
	return func(s *settings) { s.noTimeout = true }
}

// WithResponseBodyLimit caps how many bytes a response body may yield; zero means unlimited.
func WithResponseBodyLimit(n int64) Option {
	return func(s *settings) { s.responseBodyLimit = n }
}

// WithRetry installs a retrying transport; a MaxAttempts below 2 leaves the client without one.
func WithRetry(p RetryPolicy) Option {
	return func(s *settings) { s.retry = p }
}

// WithOtel toggles otelhttp transport instrumentation.
func WithOtel(enabled bool) Option {
	return func(s *settings) { s.otel = enabled }
}
