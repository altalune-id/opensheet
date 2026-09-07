package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testRetryDelay = time.Millisecond

func fastPolicy(attempts int, nonIdempotent bool) RetryPolicy {
	return RetryPolicy{
		MaxAttempts:        attempts,
		BaseDelay:          testRetryDelay,
		MaxDelay:           5 * testRetryDelay,
		RetryNonIdempotent: nonIdempotent,
	}
}

type countingServer struct {
	*httptest.Server
	hits       atomic.Int64
	bodies     chan string
	statuses   []int
	retryAfter string
}

func newCountingServer(t *testing.T, statuses ...int) *countingServer {
	t.Helper()
	cs := &countingServer{bodies: make(chan string, 16), statuses: statuses}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(cs.hits.Add(1)) - 1
		body, _ := io.ReadAll(r.Body)
		cs.bodies <- string(body)
		status := http.StatusOK
		if n < len(cs.statuses) {
			status = cs.statuses[n]
		} else if len(cs.statuses) > 0 {
			status = cs.statuses[len(cs.statuses)-1]
		}
		if cs.retryAfter != "" {
			w.Header().Set("Retry-After", cs.retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(cs.Close)
	return cs
}

func getStatus(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func postStatus(t *testing.T, c *http.Client, url, body string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestWithRetry_RetriesServerErrorThenSucceeds(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusOK)
	c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(3, false)))

	require.Equal(t, http.StatusOK, getStatus(t, c, cs.URL))
	require.Equal(t, int64(3), cs.hits.Load())
}

func TestWithRetry_StatusHandling(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantHits int64
	}{
		{"429 retries", http.StatusTooManyRequests, 3},
		{"500 retries", http.StatusInternalServerError, 3},
		{"503 retries", http.StatusServiceUnavailable, 3},
		{"501 does not retry", http.StatusNotImplemented, 1},
		{"400 does not retry", http.StatusBadRequest, 1},
		{"404 does not retry", http.StatusNotFound, 1},
		{"200 does not retry", http.StatusOK, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := newCountingServer(t, tc.status)
			c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(3, false)))

			require.Equal(t, tc.status, getStatus(t, c, cs.URL))
			require.Equal(t, tc.wantHits, cs.hits.Load())
		})
	}
}

func TestWithRetry_DisabledBelowTwoAttempts(t *testing.T) {
	for _, attempts := range []int{0, 1} {
		t.Run(fmt.Sprintf("maxAttempts=%d", attempts), func(t *testing.T) {
			cs := newCountingServer(t, http.StatusServiceUnavailable)
			c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(attempts, false)))

			getStatus(t, c, cs.URL)
			require.Equal(t, int64(1), cs.hits.Load())
		})
	}
}

func TestWithRetry_NoOptionMeansNoRetry(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	c := New(WithAllowPrivateHosts(true))

	getStatus(t, c, cs.URL)
	require.Equal(t, int64(1), cs.hits.Load())
}

func TestWithRetry_PostNeedsExplicitOptIn(t *testing.T) {
	cases := []struct {
		name          string
		nonIdempotent bool
		wantHits      int64
	}{
		{"post not replayed by default", false, 1},
		{"post replayed when opted in", true, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := newCountingServer(t, http.StatusServiceUnavailable)
			c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(3, tc.nonIdempotent)))

			postStatus(t, c, cs.URL, "payload")
			require.Equal(t, tc.wantHits, cs.hits.Load())
		})
	}
}

func TestWithRetry_ReplaysRequestBodyOnEveryAttempt(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)
	c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(3, true)))

	postStatus(t, c, cs.URL, "payload")

	require.Equal(t, int64(3), cs.hits.Load())
	close(cs.bodies)
	for body := range cs.bodies {
		require.Equal(t, "payload", body, "every attempt must send the full body")
	}
}

func TestWithRetry_UnreplayableBodyIsNotRetried(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(3, true)))

	// NOTE: a bare io.Reader gives http.NewRequest no GetBody, so the body cannot be rewound.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cs.URL, io.LimitReader(strings.NewReader("payload"), 7))
	require.NoError(t, err)
	require.Nil(t, req.GetBody, "test premise: this request must have no GetBody")

	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, int64(1), cs.hits.Load())
}

func TestWithRetry_HonoursRetryAfterHeader(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := New(WithAllowPrivateHosts(true), WithRetry(RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Second}))

	start := time.Now()
	status := getStatus(t, c, srv.URL)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, int64(2), hits.Load())
	require.GreaterOrEqual(t, elapsed, time.Second, "Retry-After must win over the 1ms base delay")
}

func TestWithRetry_RetryAfterCappedByMaxDelay(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := New(WithAllowPrivateHosts(true), WithRetry(RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 50 * time.Millisecond}))

	start := time.Now()
	getStatus(t, c, srv.URL)
	require.Less(t, time.Since(start), time.Second, "a huge Retry-After must be clamped to MaxDelay")
	require.Equal(t, int64(2), hits.Load())
}

func TestWithRetry_BackoffGrowsBetweenAttempts(t *testing.T) {
	var (
		mu     sync.Mutex
		stamps []time.Time
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		stamps = append(stamps, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := New(WithAllowPrivateHosts(true), WithRetry(RetryPolicy{MaxAttempts: 4, BaseDelay: 40 * time.Millisecond, MaxDelay: time.Second}))
	getStatus(t, c, srv.URL)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, stamps, 4)
	first := stamps[1].Sub(stamps[0])
	last := stamps[3].Sub(stamps[2])
	require.Greater(t, last, first, "backoff must grow: gaps were %v then %v", first, last)
}

func TestWithRetry_ContextCancelDuringBackoff(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	c := New(WithAllowPrivateHosts(true), WithRetry(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Second}))

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cs.URL, nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int64(1), cs.hits.Load(), "must stop retrying once the context is done")
}

func TestWithRetry_StopsBeforeUnaffordableBackoff(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	// NOTE: BaseDelay exceeds the whole timeout, so the first backoff is unaffordable on every run.
	// A delay merely close to the budget would leave a residual the next round-trip may or may not fit in.
	c := New(WithAllowPrivateHosts(true),
		WithTimeout(300*time.Millisecond),
		WithRetry(RetryPolicy{MaxAttempts: 6, BaseDelay: 500 * time.Millisecond, MaxDelay: time.Second}))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cs.URL, nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err, "an exhausted budget must yield the upstream response, not a context error")
	defer resp.Body.Close()

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "the caller must see the real status")
	require.Equal(t, int64(1), cs.hits.Load(), "a backoff the budget cannot fund must not be spent")
}

func TestWithRetry_SpendsFullLadderWhenBudgetAllows(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	c := New(WithAllowPrivateHosts(true),
		WithTimeout(10*time.Second),
		WithRetry(fastPolicy(4, false)))

	getStatus(t, c, cs.URL)
	require.Equal(t, int64(4), cs.hits.Load(), "a generous budget must not curtail the ladder")
}

func TestWithRetry_NoDeadlineRunsFullLadder(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	c := New(WithAllowPrivateHosts(true), WithoutTimeout(), WithRetry(fastPolicy(3, false)))

	getStatus(t, c, cs.URL)
	require.Equal(t, int64(3), cs.hits.Load(), "no deadline means nothing to fit the backoff into")
}

func TestRetryableResult(t *testing.T) {
	private := &PrivateAddressError{Address: "169.254.169.254:443", Addr: netip.MustParseAddr("169.254.169.254")}
	cases := []struct {
		name string
		resp *http.Response
		err  error
		want bool
	}{
		{"transport failure", nil, errors.New("connection reset"), true},
		{"wrapped transport failure", nil, fmt.Errorf("post: %w", errors.New("eof")), true},
		{"context canceled", nil, context.Canceled, false},
		{"context deadline", nil, fmt.Errorf("do: %w", context.DeadlineExceeded), false},
		{"ssrf refusal", nil, private, false},
		{"wrapped ssrf refusal", nil, fmt.Errorf("dial: %w", private), false},
		{"body too large", nil, &BodyTooLargeError{Limit: 64}, false},
		{"nil response and nil error", nil, nil, false},
		{"dns not found", nil, &net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{"wrapped dns not found", nil, fmt.Errorf("dial: %w", &net.DNSError{Err: "no such host", IsNotFound: true}), false},
		{"dns temporary still retries", nil, &net.DNSError{Err: "server misbehaving", IsTemporary: true}, true},
		{"tls verification", nil, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, false},
		{"wrapped tls verification", nil, fmt.Errorf("get: %w", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), false},
		{"408", &http.Response{StatusCode: http.StatusRequestTimeout}, nil, true},
		{"429", &http.Response{StatusCode: http.StatusTooManyRequests}, nil, true},
		{"500", &http.Response{StatusCode: http.StatusInternalServerError}, nil, true},
		{"501", &http.Response{StatusCode: http.StatusNotImplemented}, nil, false},
		{"200", &http.Response{StatusCode: http.StatusOK}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, retryableResult(tc.resp, tc.err))
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().UTC().Add(90 * time.Second).Format(http.TimeFormat)
	past := time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)
	cases := []struct {
		name  string
		in    string
		want  time.Duration
		delta time.Duration
	}{
		{"empty", "", 0, 0},
		{"seconds", "5", 5 * time.Second, 0},
		{"zero seconds", "0", 0, 0},
		{"negative seconds", "-3", 0, 0},
		{"http date in future", future, 90 * time.Second, 5 * time.Second},
		{"http date in past", past, 0, 0},
		{"garbage", "soon", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, float64(tc.want), float64(ParseRetryAfter(tc.in)), float64(tc.delta))
		})
	}
}

func TestRetryPolicy_WithDefaults(t *testing.T) {
	got := RetryPolicy{MaxAttempts: 3}.withDefaults()
	require.Equal(t, DefaultRetryBaseDelay, got.BaseDelay)
	require.Equal(t, DefaultRetryMaxDelay, got.MaxDelay)

	custom := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}.withDefaults()
	require.Equal(t, time.Second, custom.BaseDelay)
	require.Equal(t, time.Minute, custom.MaxDelay)
}

func TestRestyOptions_RetryIsDeclarative(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)
	rc := NewResty(RestyOptions{AllowPrivateHosts: true, Retry: fastPolicy(3, false)})

	resp, err := rc.R().SetContext(t.Context()).Get(cs.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	require.Equal(t, int64(3), cs.hits.Load(), "RestyOptions.Retry must drive the transport with no caller wiring")
}

func TestRestyOptions_RetryDoesNotStackWithClientRetry(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	policy := fastPolicy(3, false)
	// NOTE: the natural mistake — same policy passed both ways. It must not multiply to 9 attempts.
	rc := NewResty(RestyOptions{
		AllowPrivateHosts: true,
		Retry:             policy,
		HTTPClient:        New(WithAllowPrivateHosts(true), WithRetry(policy)),
	})

	_, err := rc.R().SetContext(t.Context()).Get(cs.URL)
	require.NoError(t, err)
	require.Equal(t, int64(3), cs.hits.Load(), "retry must not stack: want MaxAttempts, not MaxAttempts squared")
}

func TestRestyOptions_RetryWrapsSuppliedClient(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable, http.StatusOK)
	supplied := New(WithAllowPrivateHosts(true))
	rc := NewResty(RestyOptions{Retry: fastPolicy(2, false), HTTPClient: supplied})

	_, err := rc.R().SetContext(t.Context()).Get(cs.URL)
	require.NoError(t, err)
	require.Equal(t, int64(2), cs.hits.Load(), "a supplied client must still gain the retry transport")

	_, wrapped := supplied.Transport.(*retryTransport)
	require.False(t, wrapped, "the caller's own client must not be mutated")
}

func TestRestyOptions_NoRetryByDefault(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	rc := NewResty(RestyOptions{AllowPrivateHosts: true})

	_, err := rc.R().SetContext(t.Context()).Get(cs.URL)
	require.NoError(t, err)
	require.Equal(t, int64(1), cs.hits.Load())
}

func TestRestyOptions_AllowPrivateHostsGatesTheDialFilter(t *testing.T) {
	cs := newCountingServer(t, http.StatusOK)

	blocked := NewResty(RestyOptions{})
	_, err := blocked.R().SetContext(t.Context()).Get(cs.URL)
	require.Error(t, err)
	require.True(t, IsPrivateAddressError(err), "want PrivateAddressError, got %v", err)

	allowed := NewResty(RestyOptions{AllowPrivateHosts: true})
	_, err = allowed.R().SetContext(t.Context()).Get(cs.URL)
	require.NoError(t, err)
}

func TestWithRetry_OnRetryReportsEveryWaitAndTheStop(t *testing.T) {
	cs := newCountingServer(t, http.StatusServiceUnavailable)
	var (
		mu     sync.Mutex
		events []RetryAttempt
	)
	policy := fastPolicy(3, false)
	policy.OnRetry = func(a RetryAttempt) {
		mu.Lock()
		events = append(events, a)
		mu.Unlock()
	}
	c := New(WithAllowPrivateHosts(true), WithRetry(policy))

	getStatus(t, c, cs.URL)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 3, "two waits plus the exhausted stop")
	for i, e := range events {
		require.Equal(t, i, e.Index)
		require.Equal(t, http.StatusServiceUnavailable, e.StatusCode)
		require.NoError(t, e.Err)
	}
	require.Positive(t, events[0].Wait)
	require.Positive(t, events[1].Wait)
	require.Zero(t, events[2].Wait, "a zero Wait marks the ladder stopping")
}

func TestWithRetry_OnRetryMarksBudgetStop(t *testing.T) {
	// NOTE: the server sleep dominates the budget arithmetic, so the affordability check trips on
	// elapsed work rather than on a narrow margin that a loaded CI runner can overshoot.
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	var (
		mu     sync.Mutex
		events []RetryAttempt
	)
	c := New(WithAllowPrivateHosts(true),
		WithTimeout(5*time.Second),
		WithRetry(RetryPolicy{
			MaxAttempts: 6,
			BaseDelay:   50 * time.Millisecond,
			MaxDelay:    400 * time.Millisecond,
			OnRetry: func(a RetryAttempt) {
				mu.Lock()
				events = append(events, a)
				mu.Unlock()
			},
		}))

	ctx, cancel := context.WithTimeout(t.Context(), 900*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err, "the budget check must yield the upstream response, not a context error")
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	require.Zero(t, last.Wait, "the budget stop must be reported with a zero Wait")
	require.Less(t, last.Index, 5, "the ladder stopped short of its 6 attempts")
}

func TestWithRetry_FirstBackoffHonoursBaseDelay(t *testing.T) {
	tr := &retryTransport{policy: RetryPolicy{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, MaxDelay: 5 * time.Second}}
	for range 500 {
		require.GreaterOrEqual(t, tr.backoff(nil, 0), 500*time.Millisecond, "BaseDelay is a floor, not a ceiling")
	}
	for attempt := range 6 {
		require.LessOrEqual(t, tr.backoff(nil, attempt), 5*time.Second, "MaxDelay caps every wait")
	}
}

func TestRetryPolicy_MaxDelayNeverBelowBaseDelay(t *testing.T) {
	got := RetryPolicy{MaxAttempts: 3, BaseDelay: 10 * time.Second, MaxDelay: time.Second}.withDefaults()
	require.Equal(t, 10*time.Second, got.MaxDelay, "a MaxDelay under BaseDelay would make the floor unreachable")
}

func TestRestyOptions_UsesRestyNativeRetryEngine(t *testing.T) {
	rc := NewResty(RestyOptions{AllowPrivateHosts: true, Retry: fastPolicy(3, false)})

	require.Equal(t, 2, rc.RetryCount, "resty's own ladder must drive the retries")
	require.Len(t, rc.RetryConditions, 1)
	_, wrapped := rc.GetClient().Transport.(*retryTransport)
	require.False(t, wrapped, "the transport must not retry as well, or attempts would multiply")
}

type retryEngine struct {
	name string
	call func(t *testing.T, p RetryPolicy, method, url, body string) (int, error)
}

func retryEngines() []retryEngine {
	return []retryEngine{
		{"transport", func(t *testing.T, p RetryPolicy, method, url, body string) (int, error) {
			t.Helper()
			c := New(WithAllowPrivateHosts(true), WithRetry(p))
			var rdr io.Reader
			if body != "" {
				rdr = strings.NewReader(body)
			}
			req, err := http.NewRequestWithContext(t.Context(), method, url, rdr)
			require.NoError(t, err)
			resp, err := c.Do(req)
			if err != nil {
				return 0, err
			}
			defer resp.Body.Close()
			return resp.StatusCode, nil
		}},
		{"resty", func(t *testing.T, p RetryPolicy, method, url, body string) (int, error) {
			t.Helper()
			rc := NewResty(RestyOptions{AllowPrivateHosts: true, Retry: p})
			req := rc.R().SetContext(t.Context())
			if body != "" {
				req.SetBody(body)
			}
			resp, err := req.Execute(method, url)
			if err != nil {
				return 0, err
			}
			return resp.StatusCode(), nil
		}},
	}
}

// Both engines must be observably identical for the same RetryPolicy.
func TestRetryEngineConformance(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		body     string
		policy   RetryPolicy
		statuses []int
		wantHits int64
	}{
		{"5xx climbs to the cap", http.MethodGet, "", fastPolicy(3, false), []int{http.StatusServiceUnavailable}, 3},
		{"5xx stops on success", http.MethodGet, "", fastPolicy(3, false), []int{http.StatusBadGateway, http.StatusOK}, 2},
		{"429 retries", http.MethodGet, "", fastPolicy(3, false), []int{http.StatusTooManyRequests}, 3},
		{"501 does not retry", http.MethodGet, "", fastPolicy(3, false), []int{http.StatusNotImplemented}, 1},
		{"400 does not retry", http.MethodGet, "", fastPolicy(3, false), []int{http.StatusBadRequest}, 1},
		{"200 does not retry", http.MethodGet, "", fastPolicy(3, false), []int{http.StatusOK}, 1},
		{"post blocked without opt-in", http.MethodPost, "payload", fastPolicy(3, false), []int{http.StatusServiceUnavailable}, 1},
		{"post retried with opt-in", http.MethodPost, "payload", fastPolicy(3, true), []int{http.StatusServiceUnavailable}, 3},
		{"put retries without opt-in", http.MethodPut, "payload", fastPolicy(3, false), []int{http.StatusServiceUnavailable}, 3},
		{"single attempt disables retry", http.MethodGet, "", fastPolicy(1, false), []int{http.StatusServiceUnavailable}, 1},
	}
	for _, tc := range cases {
		for _, eng := range retryEngines() {
			t.Run(tc.name+"/"+eng.name, func(t *testing.T) {
				cs := newCountingServer(t, tc.statuses...)
				status, err := eng.call(t, tc.policy, tc.method, cs.URL, tc.body)

				require.NoError(t, err)
				require.Equal(t, tc.statuses[min(len(tc.statuses)-1, int(tc.wantHits)-1)], status)
				require.Equal(t, tc.wantHits, cs.hits.Load(), "engine %q diverged", eng.name)
			})
		}
	}
}

func TestRetryEngineConformance_BodyReplayedEveryAttempt(t *testing.T) {
	for _, eng := range retryEngines() {
		t.Run(eng.name, func(t *testing.T) {
			cs := newCountingServer(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)
			_, err := eng.call(t, fastPolicy(3, true), http.MethodPost, cs.URL, "payload")
			require.NoError(t, err)
			require.Equal(t, int64(3), cs.hits.Load())

			close(cs.bodies)
			for body := range cs.bodies {
				require.Equal(t, "payload", body, "engine %q dropped the body on replay", eng.name)
			}
		})
	}
}

func TestRetryEngineConformance_OnRetryReportsTheStop(t *testing.T) {
	for _, eng := range retryEngines() {
		t.Run(eng.name, func(t *testing.T) {
			cs := newCountingServer(t, http.StatusServiceUnavailable)
			var (
				mu     sync.Mutex
				events []RetryAttempt
			)
			p := fastPolicy(3, false)
			p.OnRetry = func(a RetryAttempt) {
				mu.Lock()
				events = append(events, a)
				mu.Unlock()
			}
			_, err := eng.call(t, p, http.MethodGet, cs.URL, "")
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, events, 3, "engine %q: two waits plus the stop", eng.name)
			require.Positive(t, events[0].Wait)
			require.Positive(t, events[1].Wait)
			require.Zero(t, events[2].Wait, "engine %q must mark the ladder stopping", eng.name)
			for i, e := range events {
				require.Equal(t, i, e.Index, "engine %q index", eng.name)
				require.Equal(t, http.StatusServiceUnavailable, e.StatusCode)
			}
		})
	}
}

func TestRetryEngineConformance_StopsBeforeUnaffordableBackoff(t *testing.T) {
	// NOTE: transport only. resty's ladder runs above http.Client.Do, so Client.Timeout bounds
	// each attempt rather than the ladder; see TestRestyRetry_TimeoutIsPerAttemptNotTotal.
	cs := newCountingServer(t, http.StatusTooManyRequests)
	cs.retryAfter = "30"
	p := RetryPolicy{MaxAttempts: 4, BaseDelay: 50 * time.Millisecond, MaxDelay: time.Minute}

	status, err := retryEngines()[0].call(t, p, http.MethodGet, cs.URL, "")
	require.NoError(t, err, "an exhausted budget must yield the upstream response, not a context error")
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, int64(1), cs.hits.Load(), "a 30s Retry-After cannot fit the budget")
}

// Pins the divergence rather than hiding it: with resty's engine the caller's context is the
// only thing that can bound the whole ladder, because Client.Timeout applies per attempt.
func TestRestyRetry_TimeoutIsPerAttemptNotTotal(t *testing.T) {
	cs := newCountingServer(t, http.StatusTooManyRequests)
	cs.retryAfter = "1"
	rc := NewResty(RestyOptions{
		AllowPrivateHosts: true,
		Timeout:           300 * time.Millisecond,
		Retry:             RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Second},
	})

	start := time.Now()
	_, err := rc.R().SetContext(t.Context()).Get(cs.URL)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, int64(3), cs.hits.Load(), "the 300ms Timeout does not curtail the ladder")
	require.Greater(t, elapsed, 2*time.Second, "two 1s Retry-After waits ran outside the 300ms budget")
}

func TestRestyRetry_CallerDeadlineBoundsTheLadder(t *testing.T) {
	cs := newCountingServer(t, http.StatusTooManyRequests)
	cs.retryAfter = "30"
	rc := NewResty(RestyOptions{
		AllowPrivateHosts: true,
		Retry:             RetryPolicy{MaxAttempts: 4, BaseDelay: 50 * time.Millisecond, MaxDelay: time.Minute},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	resp, err := rc.R().SetContext(ctx).Get(cs.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode())
	require.Equal(t, int64(1), cs.hits.Load(), "a caller deadline restores the affordability check")
}

func TestRetryTransport_BackoffJittersFromTheFirstAttempt(t *testing.T) {
	const (
		base     = 100 * time.Millisecond
		maxDelay = 5 * time.Second
	)
	tr := &retryTransport{policy: RetryPolicy{MaxAttempts: 5, BaseDelay: base, MaxDelay: maxDelay}}

	var prev time.Duration
	for attempt := range 3 {
		seen := make(map[time.Duration]struct{}, 512)
		lowest := maxDelay
		for range 512 {
			d := tr.backoff(nil, attempt)
			require.GreaterOrEqual(t, d, base, "attempt %d fell below BaseDelay", attempt)
			require.LessOrEqual(t, d, maxDelay, "attempt %d exceeded MaxDelay", attempt)
			seen[d] = struct{}{}
			lowest = min(lowest, d)
		}
		require.Greater(t, len(seen), 1,
			"attempt %d produced one wait for every caller, so clients retry in lockstep", attempt)
		require.GreaterOrEqual(t, lowest, prev, "attempt %d did not back off further than its predecessor", attempt)
		prev = lowest
	}
}

func TestWithRetry_UntrustedTLSIsNotRetried(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(4, false)))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, doErr := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.Error(t, doErr, "the client does not trust the httptest CA")
	require.Equal(t, int64(1), conns.Load(), "an untrusted certificate fails identically on every attempt")
}

func TestWithRetry_RetriesRequestTimeout(t *testing.T) {
	cs := newCountingServer(t, http.StatusRequestTimeout, http.StatusOK)
	c := New(WithAllowPrivateHosts(true), WithRetry(fastPolicy(3, false)))

	require.Equal(t, http.StatusOK, getStatus(t, c, cs.URL))
	require.Equal(t, int64(2), cs.hits.Load())
}

func TestRetryTransport_BackoffFixedWhenMaxDelayEqualsBaseDelay(t *testing.T) {
	const d = 500 * time.Millisecond
	tr := &retryTransport{policy: RetryPolicy{MaxAttempts: 4, BaseDelay: d, MaxDelay: d}}

	for attempt := range 3 {
		require.Equal(t, d, tr.backoff(nil, attempt),
			"a policy with no room between floor and cap asks for a fixed wait, so attempt %d cannot jitter", attempt)
	}
}

func TestNew_TimeoutBounds(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{"default", nil, DefaultTimeout},
		{"explicit", []Option{WithTimeout(3 * time.Second)}, 3 * time.Second},
		{"zero keeps the default", []Option{WithTimeout(0)}, DefaultTimeout},
		{"unbounded only when asked", []Option{WithoutTimeout()}, 0},
		{"a later WithTimeout re-bounds", []Option{WithoutTimeout(), WithTimeout(time.Second)}, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, New(tc.opts...).Timeout)
		})
	}
}

func TestRestyRetry_UnbuiltRequestDoesNotPanic(t *testing.T) {
	rc := NewResty(RestyOptions{Retry: fastPolicy(3, false)})

	require.NotPanics(t, func() {
		_, err := rc.R().SetContext(t.Context()).Get("http://[::1]:namedport/x")
		require.Error(t, err, "a malformed URL must surface as an error, not a retry")
	})
}

func TestRestyRetry_TransportErrorStillRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := srv.URL
	srv.Close()

	var attempts atomic.Int64
	p := fastPolicy(3, false)
	p.OnRetry = func(RetryAttempt) { attempts.Add(1) }
	rc := NewResty(RestyOptions{AllowPrivateHosts: true, Retry: p})

	_, err := rc.R().SetContext(t.Context()).Get(dead)
	require.Error(t, err)
	require.Positive(t, attempts.Load(), "a refused connection is still worth retrying")
}

// TestRetryPolicy_WaitForSpansItsJitterWindow asserts the jitter deterministically, on the computation
// rather than on wall-clock round-trips: an eight-sample spread of real delays clusters often enough to
// flake, while two thousand samples of waitFor cover the window every time.
func TestRetryPolicy_WaitForSpansItsJitterWindow(t *testing.T) {
	t.Parallel()
	const base = 120 * time.Millisecond
	p := RetryPolicy{MaxAttempts: 2, BaseDelay: base, MaxDelay: 5 * time.Second}.withDefaults()

	lowest, highest := time.Duration(math.MaxInt64), time.Duration(0)
	for range 2000 {
		d := p.waitFor(0, 0)
		lowest, highest = min(lowest, d), max(highest, d)
	}

	require.GreaterOrEqual(t, lowest, base, "BaseDelay is a floor: got %v", lowest)
	require.LessOrEqual(t, highest, 2*base, "the first step doubles at most: got %v", highest)
	require.Greater(t, highest-lowest, base*9/10,
		"delays spanned only %v of a %v window, so clients would retry in near-lockstep", highest-lowest, base)
}
