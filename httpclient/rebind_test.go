package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/httpclient"
)

// NOTE: regression for DNS rebinding at TTL 0 — the filter must run per dial, not once per hostname.
func TestNew_RechecksEveryDial(t *testing.T) {
	c := httpclient.New(httpclient.WithDialTimeout(500 * time.Millisecond))

	for i := range 3 {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
		require.NoError(t, err)
		resp, err := c.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		srv.Close()

		require.Error(t, err, "dial %d must be rejected", i)
		require.True(t, httpclient.IsPrivateAddressError(err), "dial %d: want PrivateAddressError, got %v", i, err)
	}
}
