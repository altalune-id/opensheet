package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Timeouts applied by NewProber unless overridden.
const (
	DefaultProbeDialTimeout = 2 * time.Second
	DefaultProbeTimeout     = 5 * time.Second
)

// Prober issues one-shot health GETs against trusted URLs.
type Prober struct {
	client *http.Client
}

// ProbeResult carries what Probe observed, and is populated even when Probe reports an error.
type ProbeResult struct {
	StatusCode int
	Elapsed    time.Duration
}

// NewProber returns a Prober tuned for readiness checks. SECURITY: TRUSTED-URL-ONLY — the SSRF filter is off, so a caller-supplied url is dialed even when it resolves to a metadata or cluster-local address.
func NewProber(opts ...Option) *Prober {
	all := make([]Option, 0, 3+len(opts))
	all = append(all,
		WithAllowPrivateHosts(true),
		WithDialTimeout(DefaultProbeDialTimeout),
		WithTimeout(DefaultProbeTimeout),
	)
	return &Prober{client: New(append(all, opts...)...)}
}

// Probe issues a GET and reports an *UnhealthyStatusError when the target answers with a non-2xx status.
func (p *Prober) Probe(ctx context.Context, url string) (ProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("httpclient: build probe request: %w", err)
	}
	start := time.Now()
	resp, err := p.client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return ProbeResult{Elapsed: elapsed}, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	res := ProbeResult{StatusCode: resp.StatusCode, Elapsed: elapsed}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, &UnhealthyStatusError{StatusCode: resp.StatusCode}
	}
	return res, nil
}
