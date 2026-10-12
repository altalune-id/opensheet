package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBase_CSPEnforced guards the hx-csp nonce gate: without an enforcing CSP header the extension strips every htmx attribute off swapped-in fragments.
func TestBase_CSPEnforced(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		reportOnly bool
		want       bool
	}{
		{"enforcing", true, false, true},
		{"report only", true, true, false},
		{"disabled", false, false, false},
		{"disabled and report only", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.Cfg.HTTP.CSP.Enabled = tc.enabled
			f.Cfg.HTTP.CSP.ReportOnly = tc.reportOnly

			d := f.Deps.Base(httptest.NewRequest(http.MethodGet, "/", nil), "t")

			require.Equal(t, tc.want, d.CSPEnforced)
		})
	}
}
