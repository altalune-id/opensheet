package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/altalune-id/oauthrs"
	"github.com/altalune-id/oidcrp"

	authv1 "altalune.id/opensheet/gen/go/auth/v1"
	"altalune.id/opensheet/internal/cli/render"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tokens"
)

const apiPath = "/api"

type remoteLogin struct {
	cfg        *config.Config
	bootClient ClientBootFn
	httpClient *http.Client
	open       func(context.Context, string) error
	out        io.Writer
	errOut     io.Writer
	store      profileStore
}

// SECURITY: the issuer and client id come from the app's own RFC 9728 document, and the token is bound to the app's API (RFC 8707).
func (l remoteLogin) run(ctx context.Context, rawURL, name string) error {
	base := strings.TrimRight(rawURL, "/")
	if err := checkServerURL(base); err != nil {
		return err
	}
	resource := base + apiPath
	meta, err := fetchAPIMetadata(ctx, l.httpClient, resource)
	if err != nil {
		return err
	}
	if err := checkIssuerURL(meta.issuer, base); err != nil {
		return err
	}
	rp := oidcrp.New(oidcrp.Config{
		Issuer: meta.issuer, ClientID: meta.clientID, RedirectURI: cliRedirectURI, Scopes: cliScopes(meta.scopes),
		Resource: resource, HTTPClient: l.httpClient,
	})
	_, _ = fmt.Fprintf(l.errOut, "Signing in through %s\n", meta.issuer)
	set, err := loopbackSignIn(ctx, rp, l.cfg.OIDC.RedirectPort, l.errOut, l.open)
	if err != nil {
		return fmt.Errorf("login: loopback: %w", err)
	}
	if set.RefreshToken == "" {
		_, _ = fmt.Fprintln(l.errOut, "warn: the issuer sent no refresh token (the CLI client needs refresh tokens on); sign in again when the access token expires")
	}
	p := profile{
		URL: base, ExpiresAt: set.ExpiresAt,
		Token: &profileToken{
			Issuer: meta.issuer, ClientID: meta.clientID, Resource: resource,
			Access: set.AccessToken, Refresh: set.RefreshToken, Scopes: set.Scopes,
		},
	}
	if set.IDToken != nil {
		p.Principal = principalView{Email: set.IDToken.Email, Name: set.IDToken.Name}
	}
	who, err := l.whoami(ctx, base, set.AccessToken)
	switch {
	case connect.CodeOf(err) == connect.CodeUnauthenticated:
		if set.RefreshToken != "" {
			_ = rp.Revoke(ctx, set.RefreshToken, "refresh_token")
		}
		return &UserNotProvisionedError{URL: base}
	case err != nil:
		_, _ = fmt.Fprintln(l.errOut, "warn: signed in, but whoami failed:", err)
	default:
		p.Principal = principalOfWhoami(who)
	}
	if err := l.save(ctx, name, p); err != nil {
		return fmt.Errorf("login: persist session: %w", err)
	}
	_, _ = fmt.Fprintf(l.out, "Signed in to %s as %s (profile %s)\n", base, cmp.Or(p.Principal.Email, p.Principal.UserID), name)
	return nil
}

// SECURITY: RFC 7009: a profile signed in again first revokes the refresh token it replaces, under the lock.
func (l remoteLogin) save(ctx context.Context, name string, p profile) error {
	return l.store.edit(ctx, func(profiles map[string]profile) (bool, error) {
		if old, ok := profiles[name]; ok && old.Token != nil && old.Token.Refresh != "" {
			revokeRefresh(ctx, l.httpClient, *old.Token, l.errOut)
		}
		profiles[name] = p
		return true, nil
	})
}

func revokeRefresh(ctx context.Context, httpClient *http.Client, t profileToken, errOut io.Writer) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	rp := oidcrp.New(oidcrp.Config{Issuer: t.Issuer, ClientID: t.ClientID, HTTPClient: httpClient})
	if err := rp.Revoke(ctx, t.Refresh, "refresh_token"); err != nil {
		_, _ = fmt.Fprintln(errOut, "warn: the refresh token was not revoked at the issuer:", err)
	}
}

func (l remoteLogin) whoami(ctx context.Context, base, token string) (*authv1.WhoamiResponse, error) {
	conn, err := dial(ctx, l.bootClient, l.cfg, base, token)
	if err != nil {
		return nil, err
	}
	resp, err := conn.Auth.Whoami(ctx, connect.NewRequest(&authv1.WhoamiRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// SECURITY: RFC 8252 Section 8.3 and RFC 9700 Section 2.6: tokens travel over https, except to a loopback host.
func checkServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return &MalformedURLError{URL: raw}
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return &InsecureURLError{URL: raw}
	}
	return nil
}

// SECURITY: a plain-http issuer is accepted only on a loopback IP literal, and only for an app that is itself on loopback.
func checkIssuerURL(issuer, base string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return &InsecureURLError{URL: issuer}
	}
	if u.Scheme == "https" {
		return nil
	}
	b, err := url.Parse(base)
	if u.Scheme == "http" && isLoopbackIP(u.Hostname()) && err == nil && isLoopbackHost(b.Hostname()) {
		return nil
	}
	return &InsecureURLError{URL: issuer}
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || isLoopbackIP(host)
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type apiMetadata struct {
	issuer   string
	clientID string
	scopes   []string
}

func fetchAPIMetadata(ctx context.Context, client *http.Client, resource string) (apiMetadata, error) {
	metaURL, err := oauthrs.MetadataURL(resource)
	if err != nil {
		return apiMetadata{}, &ResourceMetadataError{URL: resource, Reason: "not a resource identifier", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, http.NoBody)
	if err != nil {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: "build the request", Err: err}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req) //nolint:gosec // G704: the operator chose the URL.
	if err != nil {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: "fetch the RFC 9728 document", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: "answered " + resp.Status + "; is the API enabled with tokens.issuer set?"}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: "read the RFC 9728 document", Err: err}
	}
	pr, err := oauthrs.ParseProtectedResource(body)
	if err != nil {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: "not an RFC 9728 document", Err: err}
	}
	if pr.Resource != resource {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: fmt.Sprintf("names resource %q, not %q (RFC 9728 Section 3.3)", pr.Resource, resource)}
	}
	if len(pr.AuthorizationServers) != 1 {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: fmt.Sprintf("names %d authorization servers, want exactly one", len(pr.AuthorizationServers))}
	}
	clientID, ok := pr.Extra[tokens.CLIClientIDMember].(string)
	if !ok && pr.Extra[tokens.CLIClientIDMember] != nil {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: tokens.CLIClientIDMember + " is not a string"}
	}
	if clientID == "" {
		return apiMetadata{}, &ResourceMetadataError{URL: metaURL, Reason: "names no " + tokens.CLIClientIDMember + "; set oidc.cliClientID on the server"}
	}
	return apiMetadata{issuer: pr.AuthorizationServers[0], clientID: clientID, scopes: pr.ScopesSupported}, nil
}

// NOTE: the issuer narrows the API scopes to the CLI client's grant (RFC 6749 Section 3.3); offline_access asks for a refresh token.
func cliScopes(supported []string) []string {
	out := []string{"openid", "offline_access"}
	for _, s := range supported {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// SECURITY: RFC 7009: the refresh token is revoked under the session file lock, so no other process rotates it meanwhile.
func logout(ctx context.Context, store profileStore, name string, httpClient *http.Client, out, errOut io.Writer) error {
	var gone *profile
	err := store.edit(ctx, func(profiles map[string]profile) (bool, error) {
		p, ok := profiles[name]
		if !ok {
			return false, nil
		}
		if p.Token != nil && p.Token.Refresh != "" {
			revokeRefresh(ctx, httpClient, *p.Token, errOut)
		}
		delete(profiles, name)
		gone = &p
		return true, nil
	})
	switch {
	case err != nil:
		return err
	case gone == nil:
		_, _ = fmt.Fprintf(out, "not signed in (profile %s)\n", name)
	case gone.URL == "":
		_, _ = fmt.Fprintf(out, "Signed out (profile %s)\n", name)
	default:
		_, _ = fmt.Fprintf(out, "Signed out of %s (profile %s)\n", gone.URL, name)
	}
	return nil
}

func whoami(ctx context.Context, in invocation, bootClient ClientBootFn, out io.Writer, format render.Format) error {
	if in.token == "" && in.url == "" {
		p, err := in.store.load(in.profile)
		if err != nil {
			return err
		}
		if p == nil {
			return &NotSignedInError{Profile: in.profile, URL: in.baseURL(nil)}
		}
		if p.URL == "" {
			return printWhoami(out, format, in.profile, "", p.Principal)
		}
	}
	r, err := resolveRemote(ctx, in, operatorHTTPClient())
	if err != nil {
		return err
	}
	conn, err := dial(ctx, bootClient, in.cfg, r.url, r.token)
	if err != nil {
		return err
	}
	resp, err := conn.Auth.Whoami(ctx, connect.NewRequest(&authv1.WhoamiRequest{}))
	if err != nil {
		return withHint(err)
	}
	view := principalOfWhoami(resp.Msg)
	if !r.fromProfile {
		return printWhoami(out, format, "", r.url, view)
	}
	if err := in.store.edit(ctx, func(profiles map[string]profile) (bool, error) {
		cur, ok := profiles[in.profile]
		if !ok || !sameURL(cur.URL, r.url) {
			return false, nil
		}
		cur.Principal = view
		profiles[in.profile] = cur
		return true, nil
	}); err != nil {
		return err
	}
	return printWhoami(out, format, in.profile, r.url, view)
}

func principalOfWhoami(w *authv1.WhoamiResponse) principalView {
	return principalView{
		UserID: w.GetUserId(), Email: w.GetEmail(), Name: w.GetName(),
		ActiveOrgID: w.GetActiveOrgId(), ActiveOrgSlug: w.GetActiveOrgSlug(), ActiveProjectID: w.GetActiveProjectId(),
		Scopes: w.GetScopes(), Source: w.GetSource(),
	}
}

func printWhoami(out io.Writer, format render.Format, name, baseURL string, v principalView) error {
	if format == render.FormatJSON || format == render.FormatNDJSON {
		return render.JSON(out, map[string]any{
			"profile":           name,
			"url":               baseURL,
			"source":            v.Source,
			"user_id":           v.UserID,
			"email":             v.Email,
			"name":              v.Name,
			"active_org_id":     v.ActiveOrgID,
			"active_org_slug":   v.ActiveOrgSlug,
			"active_project_id": v.ActiveProjectID,
			"scopes":            v.Scopes,
		})
	}
	rows := [][2]string{
		{"profile", name}, {"url", baseURL}, {"source", v.Source}, {"user_id", v.UserID}, {"email", v.Email}, {"name", v.Name},
		{"org", v.ActiveOrgSlug}, {"org_id", v.ActiveOrgID}, {"project_id", v.ActiveProjectID}, {"scopes", strings.Join(v.Scopes, ", ")},
	}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		_, _ = fmt.Fprintf(out, "%-11s %s\n", row[0]+":", row[1])
	}
	return nil
}
