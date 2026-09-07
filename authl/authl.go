// Package authl is a small OIDC/OAuth 2.1 client toolkit exposing Start/Callback/Logout http.Handlers (web) and RunLoopback (CLI), backed by an HMAC-signed state cookie.
package authl

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config carries every knob authl needs; empty Issuer returns (nil, nil) from New for OIDC-optional deployments.
type Config struct {
	Issuer       string   // required (unless leaving OIDC unconfigured)
	ClientID     string   // required
	ClientSecret string   // empty for public / PKCE-only clients
	RedirectURL  string   // must exactly match a redirect URI registered on the OP
	Scopes       []string // default: {openid, profile, email}; add offline_access to receive a refresh token
	Resource     string   // RFC 8707 resource indicator; empty = don't send

	RememberLastUser         bool
	LastUserCookie           string        // default "authl_last_user"
	LastUserMaxAge           time.Duration // default 90d
	LastUserCookieJSReadable bool          // default false

	StateSecret []byte        // required, at least 32 bytes
	StateCookie string        // default "authl_state"
	StateMaxAge time.Duration // default 10 min

	CookieSecure bool
	CookiePath   string // default "/"

	TokenAuthStyle oauth2.AuthStyle // default AuthStyleInHeader (client_secret_basic)
	HTTPClient     *http.Client     // default: httpclient.New with private hosts allowed
}

// Identity is the verified callback result passed to OnComplete.
type Identity struct {
	Subject      string
	Email        string
	Name         string
	IDToken      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64 // unix seconds
}

// OnComplete is the caller's post-auth hook; nil means the caller wrote its own response, non-nil renders the error page.
type OnComplete func(ctx context.Context, w http.ResponseWriter, r *http.Request, ident *Identity) error

// Client is the ready OIDC client.
type Client struct {
	cfg      Config
	provider *oidc.Provider
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier

	endSessionEndpoint string
	// issParameterSupported enables the mix-up-attack check (draft-ietf-oauth-security-topics Section 4.13) only when the OP advertises it.
	issParameterSupported bool
}

// NewClient discovers the OP metadata and returns a ready [Client]; empty Issuer returns (nil, nil).
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, nil
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, errors.New("authl: ClientID is required")
	}
	if strings.TrimSpace(cfg.RedirectURL) == "" {
		return nil, errors.New("authl: RedirectURL is required")
	}
	if err := validateRedirectURL(cfg.RedirectURL); err != nil {
		return nil, err
	}
	if len(cfg.StateSecret) < 32 {
		return nil, errors.New("authl: StateSecret must be at least 32 bytes")
	}

	applyConfigDefaults(&cfg)

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, cfg.HTTPClient), cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("authl: discover %q: %w", cfg.Issuer, err)
	}

	endpoint := provider.Endpoint()
	endpoint.AuthStyle = cfg.TokenAuthStyle

	var meta struct {
		EndSessionEndpoint    string `json:"end_session_endpoint"`
		IssParameterSupported bool   `json:"authorization_response_iss_parameter_supported"`
	}
	_ = provider.Claims(&meta)

	return &Client{
		cfg:      cfg,
		provider: provider,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     endpoint,
			Scopes:       cfg.Scopes,
			RedirectURL:  cfg.RedirectURL,
		},
		verifier:              provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		endSessionEndpoint:    strings.TrimSpace(meta.EndSessionEndpoint),
		issParameterSupported: meta.IssParameterSupported,
	}, nil
}

// LastKnownUser returns the value of the hint cookie, or "" when unset.
func (c *Client) LastKnownUser(r *http.Request) string { return readCookie(r, c.cfg.LastUserCookie) }

// ClearRememberedUser removes the hint cookie without writing a redirect.
func (c *Client) ClearRememberedUser(w http.ResponseWriter, r *http.Request) {
	if c.cfg.RememberLastUser {
		c.clearLastUserCookie(w, r)
	}
}

// EndSessionURL builds the OIDC RP-Initiated Logout URL, or "" when the OP does not publish end_session_endpoint.
func (c *Client) EndSessionURL(idTokenHint, postLogoutRedirectURI, state string) string {
	if c.endSessionEndpoint == "" {
		return ""
	}
	u := *mustParseURL(c.endSessionEndpoint)
	q := u.Query()
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	if postLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	if state != "" {
		q.Set("state", state)
	}
	if c.cfg.ClientID != "" {
		q.Set("client_id", c.cfg.ClientID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// validateRedirectURL enforces RFC 6749 Section 3.1.2.1 + RFC 8252 Section 7.3: https:// everywhere, or http:// only when host is an IP loopback literal (127.0.0.1 / [::1]); "localhost" is deliberately excluded.
func validateRedirectURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("authl: RedirectURL parse: %w", err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("authl: RedirectURL must be https:// or an http://127.0.0.1/[::1] loopback; got %q", raw)
	default:
		return fmt.Errorf("authl: RedirectURL must be https:// or an http://127.0.0.1/[::1] loopback; got %q", raw)
	}
}

// verifyAzp enforces OIDC Core Section 3.1.3.7: when aud has more than one entry, azp MUST equal the RP's ClientID.
func verifyAzp(idToken *oidc.IDToken, clientID string) error {
	if len(idToken.Audience) <= 1 {
		return nil
	}
	var extra struct {
		Azp string `json:"azp"`
	}
	if err := idToken.Claims(&extra); err != nil {
		return fmt.Errorf("authl: extract azp: %w", err)
	}
	if extra.Azp != clientID {
		return fmt.Errorf("authl: multi-aud id_token azp %q does not match client_id %q", extra.Azp, clientID)
	}
	return nil
}

// GenerateStateSecret returns 32 random bytes suitable for [Config.StateSecret].
func GenerateStateSecret() ([]byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// GenerateStateSecretBase64 returns a base64url-encoded 32-byte secret for pasting into env / yaml.
func GenerateStateSecretBase64() (string, error) {
	buf, err := GenerateStateSecret()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
