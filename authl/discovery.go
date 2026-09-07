package authl

import (
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"altalune.id/template/httpclient"
)

func applyConfigDefaults(cfg *Config) {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if cfg.LastUserCookie == "" {
		cfg.LastUserCookie = "authl_last_user"
	}
	if cfg.LastUserMaxAge == 0 {
		cfg.LastUserMaxAge = 90 * 24 * time.Hour
	}
	if cfg.StateCookie == "" {
		cfg.StateCookie = "authl_state"
	}
	if cfg.StateMaxAge == 0 {
		cfg.StateMaxAge = 10 * time.Minute
	}
	if cfg.CookiePath == "" {
		cfg.CookiePath = "/"
	}
	if cfg.TokenAuthStyle == oauth2.AuthStyleAutoDetect {
		// Force HTTP Basic per RFC 6749 Section 2.3.1; oauth2's AutoDetect tries client_secret_post first.
		cfg.TokenAuthStyle = oauth2.AuthStyleInHeader
	}
	if cfg.HTTPClient == nil {
		// SECURITY: the SSRF filter stays off — the issuer is operator-configured and routinely cluster-local.
		cfg.HTTPClient = httpclient.New(httpclient.WithAllowPrivateHosts(true))
	}
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic("authl: bad end_session_endpoint URL from discovery: " + err.Error())
	}
	return u
}

func readCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
