// Package httpclient builds outbound HTTP clients that are SSRF-filtered, traced and bounded by default.
package httpclient

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Timeouts applied by New unless overridden.
const (
	DefaultDialTimeout = 10 * time.Second
	DefaultTimeout     = 30 * time.Second
)

// New returns an *http.Client that refuses non-public destinations unless WithAllowPrivateHosts(true) is passed.
func New(opts ...Option) *http.Client {
	s := settings{
		dialTimeout: DefaultDialTimeout,
		timeout:     DefaultTimeout,
		otel:        true,
	}
	for _, opt := range opts {
		opt(&s)
	}
	timeout := s.timeout
	// SECURITY: only an explicit WithoutTimeout may unbound a client; a zero WithTimeout keeps DefaultTimeout.
	if s.noTimeout {
		timeout = 0
	}
	return &http.Client{Transport: newTransport(s), Timeout: timeout}
}

func newTransport(s settings) http.RoundTripper {
	dialer := &net.Dialer{Timeout: s.dialTimeout, KeepAlive: 30 * time.Second}
	var proxy func(*http.Request) (*url.URL, error)
	// SECURITY: a proxy terminates the dial at the proxy's own address, so the filter below would never see the real destination.
	if s.allowPrivateHosts {
		proxy = http.ProxyFromEnvironment
	} else {
		dialer.ControlContext = rejectPrivate
	}

	var rt http.RoundTripper = &http.Transport{
		Proxy:       proxy,
		DialContext: dialer.DialContext,
		// NOTE: a custom DialContext disables HTTP/2 unless this is set.
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          32,
		// NOTE: the stdlib default of 2 starves concurrent calls to a single upstream.
		MaxIdleConnsPerHost: 16,
		MaxConnsPerHost:     64,
		IdleConnTimeout:     90 * time.Second,
	}
	if s.responseBodyLimit > 0 {
		rt = &limitTransport{next: rt, limit: s.responseBodyLimit}
	}
	if s.otel {
		rt = otelhttp.NewTransport(rt)
	}
	// NOTE: retry wraps the otel transport so every attempt gets its own span.
	if s.retry.enabled() {
		rt = &retryTransport{next: rt, policy: s.retry.withDefaults()}
	}
	return rt
}

type limitTransport struct {
	next  http.RoundTripper
	limit int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitReader{body: resp.Body, remaining: t.limit, limit: t.limit}
	return resp, nil
}

type limitReader struct {
	body      io.ReadCloser
	remaining int64
	limit     int64
}

func (r *limitReader) Read(p []byte) (int, error) {
	if r.remaining < 0 {
		return 0, &BodyTooLargeError{Limit: r.limit}
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.body.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return n, &BodyTooLargeError{Limit: r.limit}
	}
	return n, err
}

func (r *limitReader) Close() error { return r.body.Close() }
