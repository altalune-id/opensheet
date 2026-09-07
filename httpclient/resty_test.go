package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/httpclient"
)

func TestNewResty_Defaults(t *testing.T) {
	rc := httpclient.NewResty(httpclient.RestyOptions{})
	require.Equal(t, httpclient.DefaultRestyTimeout, rc.GetClient().Timeout)
	require.Equal(t, httpclient.DefaultUserAgent, rc.Header.Get("User-Agent"))
}

func TestNewResty_OverridesDefaults(t *testing.T) {
	rc := httpclient.NewResty(httpclient.RestyOptions{
		Timeout:   2 * time.Second,
		UserAgent: "sentec-pms/2",
	})
	require.Equal(t, 2*time.Second, rc.GetClient().Timeout)
	require.Equal(t, "sentec-pms/2", rc.Header.Get("User-Agent"))
}

func TestNewResty_SendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rc := httpclient.NewResty(httpclient.RestyOptions{
		HTTPClient: httpclient.New(httpclient.WithAllowPrivateHosts(true)),
		UserAgent:  "sentec-pms/2",
	})
	resp, err := rc.R().SetContext(t.Context()).Get(srv.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	require.Equal(t, "sentec-pms/2", got)
}

func TestNewResty_RefusesRedirectByDefault(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret-replayed"))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	rc := httpclient.NewResty(httpclient.RestyOptions{
		HTTPClient: httpclient.New(httpclient.WithAllowPrivateHosts(true)),
	})
	_, err := rc.R().SetContext(t.Context()).Get(srv.URL)
	require.Error(t, err, "a 307 from a token endpoint must not replay credentials to the redirect target")
}

func TestNewResty_FollowsRedirectWhenAllowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("followed"))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	rc := httpclient.NewResty(httpclient.RestyOptions{
		HTTPClient:     httpclient.New(httpclient.WithAllowPrivateHosts(true)),
		AllowRedirects: true,
	})
	resp, err := rc.R().SetContext(t.Context()).Get(srv.URL)
	require.NoError(t, err)
	require.Equal(t, "followed", resp.String())
}

func TestNewResty_InheritsSafeTransportWhenNoClientGiven(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rc := httpclient.NewResty(httpclient.RestyOptions{})
	_, err := rc.R().SetContext(t.Context()).Get(srv.URL)
	require.Error(t, err)
	require.True(t, httpclient.IsPrivateAddressError(err), "want PrivateAddressError, got %v", err)
}

func TestNewResty_ResponseBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	rc := httpclient.NewResty(httpclient.RestyOptions{
		HTTPClient:        httpclient.New(httpclient.WithAllowPrivateHosts(true)),
		ResponseBodyLimit: 16,
	})
	_, err := rc.R().SetContext(t.Context()).Get(srv.URL)
	require.Error(t, err, "a 4KB body must not be buffered under a 16 byte cap")
}

func TestNewResty_BackoffHonoursBaseDelayFloor(t *testing.T) {
	const base = 200 * time.Millisecond

	for run := range 5 {
		var (
			mu     sync.Mutex
			stamps []time.Time
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			stamps = append(stamps, time.Now())
			n := len(stamps)
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))

		rc := httpclient.NewResty(httpclient.RestyOptions{
			AllowPrivateHosts: true,
			Retry:             httpclient.RetryPolicy{MaxAttempts: 2, BaseDelay: base, MaxDelay: 5 * time.Second},
		})
		_, err := rc.R().SetContext(t.Context()).Get(srv.URL)
		require.NoError(t, err)
		srv.Close()

		mu.Lock()
		require.Len(t, stamps, 2)
		gap := stamps[1].Sub(stamps[0])
		mu.Unlock()
		require.GreaterOrEqual(t, gap, base, "run %d retried after %v; BaseDelay is a floor on this path too", run, gap)
	}
}
