package httpclient_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/httpclient"
)

func TestNew_Defaults(t *testing.T) {
	c := httpclient.New()
	require.Equal(t, httpclient.DefaultTimeout, c.Timeout)
	require.NotNil(t, c.Transport)
}

func TestNew_WithTimeoutOverridesDefault(t *testing.T) {
	c := httpclient.New(httpclient.WithTimeout(3 * time.Second))
	require.Equal(t, 3*time.Second, c.Timeout)
}

func TestNew_TransportForcesHTTP2(t *testing.T) {
	c := httpclient.New(httpclient.WithOtel(false))
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok, "expected a *http.Transport, got %T", c.Transport)
	require.True(t, tr.ForceAttemptHTTP2, "a custom DialContext disables HTTP/2 unless ForceAttemptHTTP2 is set")
}

func TestNew_TransportTuning(t *testing.T) {
	c := httpclient.New(httpclient.WithOtel(false), httpclient.WithDialTimeout(4*time.Second))
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok)
	require.Greater(t, tr.MaxIdleConnsPerHost, 2, "stdlib default of 2 starves concurrent calls to one upstream")
	require.NotZero(t, tr.TLSHandshakeTimeout)
	require.NotZero(t, tr.IdleConnTimeout)
}

func TestNew_ProxyDisabledWhileFilterOn(t *testing.T) {
	filtered := httpclient.New(httpclient.WithOtel(false))
	tr, ok := filtered.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, tr.Proxy, "a proxy terminates the dial at the proxy address, so the SSRF filter would never see the destination")

	trusted := httpclient.New(httpclient.WithOtel(false), httpclient.WithAllowPrivateHosts(true))
	tr, ok = trusted.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tr.Proxy, "trusted-URL clients keep ProxyFromEnvironment")
}

func TestNew_OtelWrapsTransport(t *testing.T) {
	c := httpclient.New(httpclient.WithOtel(true))
	_, ok := c.Transport.(*http.Transport)
	require.False(t, ok, "otel enabled should wrap the transport")
}

func TestNew_RefusesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := httpclient.New().Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	require.True(t, httpclient.IsPrivateAddressError(err), "want PrivateAddressError, got %v", err)
}

func TestNew_AllowPrivateHostsReachesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := httpclient.New(httpclient.WithAllowPrivateHosts(true)).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
}

func TestNew_ResponseBodyLimit(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		limit   int64
		wantErr bool
	}{
		{"under limit", 8, 16, false},
		{"exactly at limit", 16, 16, false},
		{"one byte over limit", 17, 16, true},
		{"far over limit", 4096, 16, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", tt.size)))
			}))
			defer srv.Close()

			c := httpclient.New(
				httpclient.WithAllowPrivateHosts(true),
				httpclient.WithResponseBodyLimit(tt.limit),
			)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			require.NoError(t, err)
			resp, err := c.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			body, readErr := io.ReadAll(resp.Body)
			if !tt.wantErr {
				require.NoError(t, readErr)
				require.Len(t, body, tt.size)
				return
			}
			require.Error(t, readErr)
			require.True(t, httpclient.IsBodyTooLargeError(readErr), "want BodyTooLargeError, got %v", readErr)
		})
	}
}

func TestNew_NoResponseBodyLimitByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1<<20)))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := httpclient.New(httpclient.WithAllowPrivateHosts(true)).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, body, 1<<20)
}
