package httpclient

import (
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
)

func applyRestyRetry(rc *resty.Client, p RetryPolicy) {
	rc.SetRetryCount(p.MaxAttempts - 1).
		SetRetryWaitTime(p.BaseDelay).
		SetRetryMaxWaitTime(p.MaxDelay).
		SetRetryResetReaders(true).
		SetRetryAfter(restyRetryAfter(p)).
		AddRetryCondition(restyRetryCondition(p))
}

// NOTE: resty falls back to its own un-jittered first step whenever this returns zero, so every branch must yield a positive wait.
func restyRetryAfter(p RetryPolicy) resty.RetryAfterFunc {
	return func(_ *resty.Client, resp *resty.Response) (time.Duration, error) {
		return p.waitFor(restyAttemptIndex(resp), restyResponseRetryAfter(resp)), nil
	}
}

func restyAttemptIndex(resp *resty.Response) int {
	if resp == nil || resp.Request == nil {
		return 0
	}
	return max(resp.Request.Attempt-1, 0)
}

func restyResponseRetryAfter(resp *resty.Response) time.Duration {
	if resp == nil {
		return 0
	}
	return ParseRetryAfter(resp.Header().Get("Retry-After"))
}

// SECURITY: resty applies a condition to every method, so the idempotency check below is the only thing keeping POST off the ladder.
func restyRetryCondition(p RetryPolicy) resty.RetryConditionFunc {
	return func(resp *resty.Response, err error) bool {
		// NOTE: resty hands back a nil response only when the request was never built, and its own
		// SetRetryResetReaders path dereferences that nil — so this branch must never retry.
		if resp == nil || resp.Request == nil {
			return false
		}
		if !methodReplayable(p, resp.Request.Method) {
			return false
		}
		if !retryableResult(resp.RawResponse, err) {
			return false
		}
		if resp.Request.Attempt >= p.MaxAttempts {
			notifyRetry(p, resp, err, 0)
			return false
		}
		wait := restyNextWait(p, resp)
		// NOTE: spending a backoff the budget cannot follow with another attempt trades a real response for a context error.
		if !affordable(resp.Request.Context(), wait+resp.Time()) {
			notifyRetry(p, resp, err, 0)
			return false
		}
		notifyRetry(p, resp, err, wait)
		return true
	}
}

func restyNextWait(p RetryPolicy, resp *resty.Response) time.Duration {
	return p.worstCaseWaitFor(restyAttemptIndex(resp), restyResponseRetryAfter(resp))
}

func notifyRetry(p RetryPolicy, resp *resty.Response, err error, wait time.Duration) {
	if p.OnRetry == nil {
		return
	}
	index := restyAttemptIndex(resp)
	status := 0
	if resp != nil {
		status = resp.StatusCode()
	}
	p.OnRetry(RetryAttempt{Index: index, StatusCode: status, Err: err, Wait: wait})
}

func hasRetryTransport(c *http.Client) bool {
	_, ok := c.Transport.(*retryTransport)
	return ok
}
