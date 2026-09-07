package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/httpclient"
)

func TestProber_Probe(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantStatus int
		wantErr    bool
	}{
		{"ok", http.StatusOK, http.StatusOK, false},
		{"no content", http.StatusNoContent, http.StatusNoContent, false},
		{"moved", http.StatusMovedPermanently, http.StatusMovedPermanently, true},
		{"server error", http.StatusInternalServerError, http.StatusInternalServerError, true},
		{"unavailable", http.StatusServiceUnavailable, http.StatusServiceUnavailable, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			res, err := httpclient.NewProber().Probe(t.Context(), srv.URL)
			require.Equal(t, tt.wantStatus, res.StatusCode)
			require.Positive(t, res.Elapsed)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, httpclient.IsUnhealthyStatusError(err), "want UnhealthyStatusError, got %v", err)
		})
	}
}

func TestProber_ProbeUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close()

	res, err := httpclient.NewProber().Probe(t.Context(), url)
	require.Error(t, err)
	require.False(t, httpclient.IsUnhealthyStatusError(err), "a transport failure is not an unhealthy status")
	require.Zero(t, res.StatusCode)
}

func TestProber_ProbeBadURL(t *testing.T) {
	_, err := httpclient.NewProber().Probe(t.Context(), "http://[::1]:namedport/")
	require.Error(t, err)
}

func TestProber_AllowsLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res, err := httpclient.NewProber().Probe(t.Context(), srv.URL)
	require.NoError(t, err, "probes target cluster-local addresses, so the SSRF filter must be off")
	require.Equal(t, http.StatusOK, res.StatusCode)
}
