package httpclient

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Backoff bounds applied by WithRetry unless RetryPolicy overrides them.
const (
	DefaultRetryBaseDelay = 500 * time.Millisecond
	DefaultRetryMaxDelay  = 5 * time.Second
)

const retryDrainLimit = 4 << 10

// RetryPolicy configures the retrying transport installed by WithRetry.
type RetryPolicy struct {
	MaxAttempts        int
	BaseDelay          time.Duration
	MaxDelay           time.Duration
	RetryNonIdempotent bool
	OnRetry            func(RetryAttempt)
}

// RetryAttempt reports one retryable attempt to RetryPolicy.OnRetry; a zero Wait means the ladder stopped here.
type RetryAttempt struct {
	Index      int
	StatusCode int
	Err        error
	Wait       time.Duration
}

func (p RetryPolicy) enabled() bool { return p.MaxAttempts > 1 }

func (p RetryPolicy) clamp(d time.Duration) time.Duration {
	return min(max(d, p.BaseDelay), p.MaxDelay)
}

func (p RetryPolicy) step(attempt int) float64 {
	return min(float64(p.BaseDelay)*math.Exp2(float64(attempt)), float64(p.MaxDelay)/2)
}

func (p RetryPolicy) waitFor(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return p.clamp(retryAfter)
	}
	step := p.step(attempt)
	return p.clamp(time.Duration(step + rand.Float64()*step))
}

func (p RetryPolicy) worstCaseWaitFor(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return p.clamp(retryAfter)
	}
	return p.clamp(time.Duration(2 * p.step(attempt)))
}

func (p RetryPolicy) withDefaults() RetryPolicy {
	p.BaseDelay = cmp.Or(p.BaseDelay, DefaultRetryBaseDelay)
	p.MaxDelay = max(cmp.Or(p.MaxDelay, DefaultRetryMaxDelay), p.BaseDelay)
	return p
}

type retryTransport struct {
	next   http.RoundTripper
	policy RetryPolicy
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.replayable(req) {
		return t.next.RoundTrip(req)
	}
	var (
		resp *http.Response
		err  error
	)
	attempt := req
	for i := range t.policy.MaxAttempts {
		if i > 0 {
			if attempt, err = rewind(req); err != nil {
				return nil, err
			}
		}
		started := time.Now()
		resp, err = t.next.RoundTrip(attempt)
		spent := time.Since(started)
		if !retryableResult(resp, err) {
			break
		}
		if i == t.policy.MaxAttempts-1 {
			t.notify(i, resp, err, 0)
			break
		}
		wait := t.backoff(resp, i)
		// NOTE: spending a backoff the budget cannot follow with another attempt trades a real response for a context error.
		if !affordable(req.Context(), wait+spent) {
			t.notify(i, resp, err, 0)
			break
		}
		t.notify(i, resp, err, wait)
		drain(resp)
		if werr := sleep(req.Context(), wait); werr != nil {
			return nil, werr
		}
	}
	return resp, err
}

func (t *retryTransport) replayable(req *http.Request) bool {
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return false
	}
	return methodReplayable(t.policy, req.Method)
}

// SECURITY: replaying POST or PATCH can duplicate a side effect, so only a caller that sends an idempotency key may opt in.
func methodReplayable(p RetryPolicy, method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	}
	return p.RetryNonIdempotent
}

func (t *retryTransport) backoff(resp *http.Response, attempt int) time.Duration {
	return t.policy.waitFor(attempt, responseRetryAfter(resp))
}

func responseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	return ParseRetryAfter(resp.Header.Get("Retry-After"))
}

func (t *retryTransport) notify(index int, resp *http.Response, err error, wait time.Duration) {
	if t.policy.OnRetry == nil {
		return
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	t.policy.OnRetry(RetryAttempt{Index: index, StatusCode: status, Err: err, Wait: wait})
}

func retryableResult(resp *http.Response, err error) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		// NOTE: each guard below fails identically on every attempt, so retrying only burns the budget.
		return !IsPrivateAddressError(err) && !IsBodyTooLargeError(err) && !permanentTransportError(err)
	}
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return resp.StatusCode >= http.StatusInternalServerError && resp.StatusCode != http.StatusNotImplemented
}

func permanentTransportError(err error) bool {
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns) && dns.IsNotFound
}

func rewind(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.GetBody == nil {
		return clone, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}

func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, retryDrainLimit))
	_ = resp.Body.Close()
}

func affordable(ctx context.Context, need time.Duration) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) > need
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ParseRetryAfter reads a Retry-After header in either delay-seconds or HTTP-date form, returning 0 when absent or already elapsed.
func ParseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	return max(time.Until(t), 0)
}
