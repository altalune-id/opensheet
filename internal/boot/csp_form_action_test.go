package boot_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/config"
)

func formAction(t *testing.T, issuer string, endSession bool, path string) string {
	t.Helper()
	cfg := newSmokeCfg(t)
	cfg.HTTP.CSP.Enabled = true
	cfg.OIDC.Issuer = issuer
	cfg.OIDC.ClientID = "app"
	cfg.OIDC.EndSessionOnLogout = endSession
	return formActionFor(t, cfg, path)
}

func formActionFor(t *testing.T, cfg *config.Config, path string) string {
	t.Helper()
	srv, err := boot.BootServer(context.Background(), cfg, boot.WithLogger(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	for directive := range strings.SplitSeq(rec.Header().Get("Content-Security-Policy"), ";") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(directive), "form-action "); ok {
			return rest
		}
	}
	t.Fatalf("%s answered without a form-action directive", path)
	return ""
}

// TestCSP_FormActionAllowsTheIssuerForLogout pins that POST /logout can follow its 303 to the issuer's end-session endpoint.
func TestCSP_FormActionAllowsTheIssuerForLogout(t *testing.T) {
	for _, path := range []string{"/login", "/"} {
		t.Run(path, func(t *testing.T) {
			require.Equal(t, "'self' https://id.example.com:8443", formAction(t, "https://id.example.com:8443/tenant", true, path))
			require.Equal(t, "'self'", formAction(t, "https://id.example.com:8443/tenant", false, path))
			require.Equal(t, "'self'", formAction(t, "", true, path))
		})
	}
}

// TestCSP_FormActionAllowsGoogleConsentOnlyWithAClient pins that the Google connect POST can follow its 303 to Google's consent screen, and that nothing else is widened.
func TestCSP_FormActionAllowsGoogleConsentOnlyWithAClient(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.HTTP.CSP.Enabled = true
	cfg.Google.OAuth.ClientID = "123-abc.apps.googleusercontent.com"
	cfg.Google.OAuth.ClientSecret = "google-secret"
	require.Equal(t, "'self' https://accounts.google.com", formActionFor(t, cfg, "/login"))

	require.Equal(t, "'self'", formAction(t, "", false, "/login"))
}
