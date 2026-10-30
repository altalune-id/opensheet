package cli

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"altalune.id/opensheet/httpclient"
	"altalune.id/opensheet/internal/platform/config"
)

const (
	defaultProfile = "default"
	issuerTimeout  = 30 * time.Second
	responseLimit  = 1 << 20
)

// NOTE: precedence is --profile > OPENSHEET_PROFILE > "default".
func profileName(cmd *cobra.Command) string {
	if f := cmd.Root().PersistentFlags().Lookup("profile"); f != nil && f.Changed {
		if v := strings.TrimSpace(f.Value.String()); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv(envVar("PROFILE"))); v != "" {
		return v
	}
	return defaultProfile
}

func sessionStore(cmd *cobra.Command) (profileStore, error) {
	cfg := configFromCtx(cmd.Context())
	if cfg == nil || cfg.Session.Path == "" {
		return profileStore{}, errors.New("cli: session.path is not set")
	}
	return profileStore{path: cfg.Session.Path}, nil
}

// SECURITY: the operator chose every URL this client reaches, so private hosts are allowed; redirects are never followed.
func operatorHTTPClient() *http.Client {
	c := httpclient.New(
		httpclient.WithAllowPrivateHosts(true),
		httpclient.WithTimeout(issuerTimeout),
		httpclient.WithResponseBodyLimit(responseLimit),
	)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

type invocation struct {
	cfg     *config.Config
	store   profileStore
	profile string
	url     string
	token   string
}

func invocationFrom(cmd *cobra.Command) (invocation, error) {
	cfg, err := withCfg(cmd)
	if err != nil {
		return invocation{}, err
	}
	store, err := sessionStore(cmd)
	if err != nil {
		return invocation{}, err
	}
	token, err := resolveToken(cmd)
	if err != nil {
		return invocation{}, err
	}
	return invocation{cfg: cfg, store: store, profile: profileName(cmd), url: explicitURL(cmd), token: token}, nil
}

// NOTE: precedence is --url > OPENSHEET_URL > the profile's URL > config http.baseURL.
func (in invocation) baseURL(p *profile) string {
	if in.url != "" {
		return in.url
	}
	if p != nil && p.URL != "" {
		return p.URL
	}
	if in.cfg != nil {
		return strings.TrimSpace(in.cfg.HTTP.BaseURL)
	}
	return ""
}

type remote struct {
	url         string
	token       string
	fromProfile bool
}

// SECURITY: precedence is --token > --token-file > OPENSHEET_TOKEN > the profile's token, sent only to the profile's own URL.
func resolveRemote(ctx context.Context, in invocation, httpClient *http.Client) (remote, error) {
	if in.token != "" {
		return in.explicitRemote()
	}
	p, err := in.store.load(in.profile)
	if err != nil {
		return remote{}, err
	}
	url := in.baseURL(p)
	if url == "" {
		return remote{}, &MissingURLError{}
	}
	if err := checkServerURL(url); err != nil {
		return remote{}, err
	}
	src := tokenSource{store: in.store, name: in.profile, httpClient: httpClient, now: time.Now}
	access, err := src.accessToken(ctx, url)
	if err != nil {
		return remote{}, err
	}
	return remote{url: url, token: access, fromProfile: true}, nil
}

// NOTE: an explicit token reads the session file only for a URL, and only when none was given, so a broken file never blocks it.
func (in invocation) explicitRemote() (remote, error) {
	var p *profile
	if in.url == "" {
		p, _ = in.store.load(in.profile)
	}
	url := in.baseURL(p)
	if url == "" {
		return remote{}, &MissingURLError{}
	}
	// SECURITY: RFC 6750 Section 5.3: a bearer token never travels over plain http to a remote host.
	if err := checkServerURL(url); err != nil {
		return remote{}, err
	}
	return remote{url: url, token: in.token}, nil
}
