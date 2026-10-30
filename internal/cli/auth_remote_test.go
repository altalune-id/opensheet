package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/altalune-id/oauthrs"
	"github.com/altalune-id/oidcrp/oidcrptest"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	authv1 "altalune.id/opensheet/gen/go/auth/v1"
	"altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

const (
	testCLIClient = "cli-client"
	testEmail     = "ana@gmail.com"
)

type fakeApp struct {
	*httptest.Server
	resource  string
	projectID string
	known     atomic.Bool
	verifier  *oauthrs.Verifier
}

func (a *fakeApp) Whoami(ctx context.Context, req *connect.Request[authv1.WhoamiRequest]) (*connect.Response[authv1.WhoamiResponse], error) {
	raw, ok := strings.CutPrefix(req.Header().Get("Authorization"), "Bearer ")
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	tok, err := a.verifier.Verify(ctx, raw)
	if err != nil || !a.known.Load() {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return connect.NewResponse(&authv1.WhoamiResponse{
		UserId: uuid.NewSHA1(uuid.NameSpaceURL, []byte(tok.Subject)).String(), Email: testEmail,
		ActiveProjectId: a.projectID, Scopes: tok.Scopes, Source: string(session.SourceToken),
	}), nil
}

func newFakeApp(t *testing.T, idp *oidcrptest.IDP, cliClientID string) *fakeApp {
	t.Helper()
	app := &fakeApp{projectID: uuid.NewString()}
	app.known.Store(true)
	mux := http.NewServeMux()
	app.Server = httptest.NewServer(mux)
	t.Cleanup(app.Close)
	app.resource = app.URL + apiPath

	res, err := tokens.NewResource(app.resource)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle(res.MetadataPath, res.CLIMetadataHandler(idp.Issuer(), []string{"apikeys:read", "posts:read"}, cliClientID))
	app.verifier, err = oauthrs.New(oauthrs.Config{Issuer: idp.Issuer(), Audience: app.resource, HTTPClient: idp.Client()})
	if err != nil {
		t.Fatal(err)
	}
	path, handler := authv1connect.NewAuthServiceHandler(app)
	mux.Handle(apiPath+path, http.StripPrefix(apiPath, handler))
	return app
}

type browser struct {
	mu   sync.Mutex
	urls []string
}

func (b *browser) open(idp *oidcrptest.IDP) func(context.Context, string) error {
	return func(ctx context.Context, authURL string) error {
		b.mu.Lock()
		b.urls = append(b.urls, authURL)
		b.mu.Unlock()
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redirect, err := url.Parse(u.Query().Get("redirect_uri"))
		if err != nil {
			return err
		}
		redirect.RawQuery = idp.Authorize(authURL).Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, redirect.String(), http.NoBody)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
}

func (b *browser) lastQuery(t *testing.T) url.Values {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.urls) == 0 {
		t.Fatal("the browser was never opened")
	}
	u, err := url.Parse(b.urls[len(b.urls)-1])
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

type issuerTraffic struct {
	next      http.RoundTripper
	refreshes atomic.Int32
	delay     time.Duration
	mangle    atomic.Bool
	revokeErr atomic.Bool
}

func (r *issuerTraffic) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/revoke") && r.revokeErr.Load() {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
	}
	refresh := false
	if strings.HasSuffix(req.URL.Path, "/token") && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		if q, _ := url.ParseQuery(string(body)); q.Get("grant_type") == "refresh_token" {
			refresh = true
			r.refreshes.Add(1)
			time.Sleep(r.delay)
		}
	}
	resp, err := r.next.RoundTrip(req)
	if err != nil || !refresh || !r.mangle.Load() || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	out["token_type"] = "mac"
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	resp.ContentLength = int64(len(b))
	return resp, nil
}

type remoteFixture struct {
	idp     *oidcrptest.IDP
	app     *fakeApp
	traffic *issuerTraffic
	client  *http.Client
	store   profileStore
	browser *browser
	out     bytes.Buffer
	errOut  bytes.Buffer
}

func newRemoteFixture(t *testing.T, cliClientID string) *remoteFixture {
	t.Helper()
	idp := oidcrptest.New(t)
	idp.User.Email = testEmail
	f := &remoteFixture{
		idp: idp, app: newFakeApp(t, idp, cliClientID), traffic: &issuerTraffic{next: idp.Client().Transport},
		store: profileStore{path: filepath.Join(t.TempDir(), "session.json")}, browser: &browser{},
	}
	f.client = &http.Client{Transport: f.traffic}
	return f
}

func (f *remoteFixture) login(t *testing.T, rawURL, name string) error {
	t.Helper()
	l := remoteLogin{
		cfg: config.Defaults(), bootClient: boot.BootClient, httpClient: f.client, open: f.browser.open(f.idp),
		out: &f.out, errOut: &f.errOut, store: f.store,
	}
	return l.run(t.Context(), rawURL, name)
}

func (f *remoteFixture) signedIn(t *testing.T, name string) profile {
	t.Helper()
	if err := f.login(t, f.app.URL, name); err != nil {
		t.Fatalf("login: %v (stderr %q)", err, f.errOut.String())
	}
	return f.profile(t, name)
}

func (f *remoteFixture) profile(t *testing.T, name string) profile {
	t.Helper()
	p, err := f.store.load(name)
	if err != nil || p == nil {
		t.Fatalf("profile %s: %+v, %v", name, p, err)
	}
	return *p
}

func (f *remoteFixture) source(name string, now func() time.Time) tokenSource {
	return tokenSource{store: f.store, name: name, httpClient: f.client, now: now}
}

func (f *remoteFixture) expire(t *testing.T, name string) {
	t.Helper()
	p := f.profile(t, name)
	p.ExpiresAt = time.Now().Add(-time.Minute)
	writeRemoteProfile(t, f.store.path, name, p)
}

func TestRemoteLogin_SignsInThroughTheIssuerForTheAPI(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	p := f.signedIn(t, "prod")

	if p.URL != f.app.URL || p.Token == nil {
		t.Fatalf("profile = %+v", p)
	}
	tok := p.Token
	if tok.Issuer != f.idp.Issuer() || tok.ClientID != testCLIClient || tok.Resource != f.app.resource {
		t.Fatalf("token = %+v", tok)
	}
	if tok.Access == "" || tok.Refresh == "" || !p.ExpiresAt.After(time.Now()) {
		t.Fatalf("login must store an access token, a refresh token and its expiry: %+v exp %v", tok, p.ExpiresAt)
	}
	if p.Principal.Email != testEmail || p.Principal.ActiveProjectID != f.app.projectID {
		t.Fatalf("principal = %+v, want the server's Whoami", p.Principal)
	}
	if got := f.idp.Resources(); got.Authorize != f.app.resource || got.Code != f.app.resource {
		t.Fatalf("RFC 8707: resources sent = %+v, want %s on authorize and code", got, f.app.resource)
	}
	q := f.browser.lastQuery(t)
	if q.Get("client_id") != testCLIClient {
		t.Fatalf("client_id = %q, want the one the API document names", q.Get("client_id"))
	}
	for _, scope := range []string{"openid", "offline_access", "apikeys:read"} {
		if !strings.Contains(" "+q.Get("scope")+" ", " "+scope+" ") {
			t.Fatalf("scope %q missing from %q", scope, q.Get("scope"))
		}
	}
	if !strings.Contains(f.out.String(), "Signed in to "+f.app.URL+" as "+testEmail+" (profile prod)") {
		t.Fatalf("stdout = %q", f.out.String())
	}
	if strings.Contains(f.out.String()+f.errOut.String(), tok.Access) || strings.Contains(f.out.String()+f.errOut.String(), tok.Refresh) {
		t.Fatal("SECURITY: a token was printed")
	}
}

func TestRemoteLogin_UnknownUserIsAClearErrorAndSavesNothing(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.app.known.Store(false)

	err := f.login(t, f.app.URL, "prod")
	if !IsUserNotProvisionedError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v (exit %d), want UserNotProvisionedError exiting 2", err, ExitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "sign in once at "+f.app.URL) {
		t.Fatalf("the error must tell the person to sign in on the web first: %v", err)
	}
	if p, _ := f.store.load("prod"); p != nil {
		t.Fatalf("a sign-in the app refused was saved: %+v", p)
	}
	if f.idp.Revocations() != 1 {
		t.Fatalf("revocations = %d, want the unused refresh token revoked", f.idp.Revocations())
	}
}

func TestRemoteLogin_RefusesAnUnusableServer(t *testing.T) {
	t.Run("plain http to a public host", func(t *testing.T) {
		f := newRemoteFixture(t, testCLIClient)
		err := f.login(t, "http://app.example.com", "prod")
		if !IsInsecureURLError(err) || ExitCodeFor(err) != ExitInvalidArg {
			t.Fatalf("err = %v, want InsecureURLError exiting 4", err)
		}
	})
	t.Run("no cli_client_id in the API document", func(t *testing.T) {
		f := newRemoteFixture(t, "")
		err := f.login(t, f.app.URL, "prod")
		if !IsResourceMetadataError(err) || !strings.Contains(err.Error(), "oidc.cliClientID") {
			t.Fatalf("err = %v, want a ResourceMetadataError naming oidc.cliClientID", err)
		}
	})
	t.Run("a server without the API document", func(t *testing.T) {
		f := newRemoteFixture(t, testCLIClient)
		err := f.login(t, f.app.URL+"/other", "prod")
		if !IsResourceMetadataError(err) {
			t.Fatalf("err = %v, want a ResourceMetadataError", err)
		}
	})
}

func TestFetchAPIMetadata_RequiresTheExactResourceAndOneIssuer(t *testing.T) {
	tests := map[string]string{
		"wrong resource":      `{"resource":"https://evil.example/api","authorization_servers":["https://idp"],"cli_client_id":"c"}`,
		"two issuers":         `{"resource":"%s","authorization_servers":["https://a","https://b"],"cli_client_id":"c"}`,
		"client id not text":  `{"resource":"%s","authorization_servers":["https://a"],"cli_client_id":7}`,
		"client id mis-cased": `{"resource":"%s","authorization_servers":["https://a"],"CLI_Client_ID":"c"}`,
		"not json":            `nope`,
		"empty body":          ``,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			var resource string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.ReplaceAll(body, "%s", resource))
			}))
			t.Cleanup(srv.Close)
			resource = srv.URL + apiPath
			if _, err := fetchAPIMetadata(t.Context(), http.DefaultClient, resource); !IsResourceMetadataError(err) {
				t.Fatalf("err = %v, want a ResourceMetadataError", err)
			}
		})
	}
}

func TestTokenSource_UsesAFreshTokenWithoutRefreshing(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	p := f.signedIn(t, "prod")

	got, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL+"/")
	if err != nil || got != p.Token.Access {
		t.Fatalf("accessToken = %q, %v; want the stored token", got, err)
	}
	if n := f.traffic.refreshes.Load(); n != 0 {
		t.Fatalf("refreshes = %d, want none for an unexpired token", n)
	}
}

func TestTokenSource_RefreshesWithinTheSkewAndRotates(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	p := f.signedIn(t, "prod")

	early := func() time.Time { return p.ExpiresAt.Add(-2 * refreshSkew) }
	if _, err := f.source("prod", early).accessToken(t.Context(), f.app.URL); err != nil || f.traffic.refreshes.Load() != 0 {
		t.Fatalf("well before expiry: err %v, refreshes %d", err, f.traffic.refreshes.Load())
	}

	late := func() time.Time { return p.ExpiresAt.Add(-refreshSkew / 2) }
	got, err := f.source("prod", late).accessToken(t.Context(), f.app.URL)
	if err != nil {
		t.Fatalf("accessToken: %v", err)
	}
	after := f.profile(t, "prod")
	if f.traffic.refreshes.Load() != 1 || got == p.Token.Access || got != after.Token.Access {
		t.Fatalf("refreshes %d, token changed %v, stored %v", f.traffic.refreshes.Load(), got != p.Token.Access, got == after.Token.Access)
	}
	if after.Token.Refresh == p.Token.Refresh || after.Token.Refresh == "" {
		t.Fatal("RFC 9700 Section 4.14.2: the rotated refresh token must replace the spent one")
	}
	if f.idp.Resources().Refresh != f.app.resource {
		t.Fatalf("RFC 8707: refresh sent resource %q", f.idp.Resources().Refresh)
	}
	if _, err := f.app.verifier.Verify(t.Context(), got); err != nil {
		t.Fatalf("the refreshed token must still be for the API: %v", err)
	}
}

func TestTokenSource_ConcurrentRefreshersShareOneRefresh(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "prod")
	f.expire(t, "prod")
	f.traffic.delay = 200 * time.Millisecond

	const n = 2
	var wg sync.WaitGroup
	tokens := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			tokens[i], errs[i] = f.source("prod", time.Now).accessToken(context.Background(), f.app.URL)
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("refresher %d: %v — a second refresh of the spent token would revoke the grant", i, errs[i])
		}
	}
	if got := f.traffic.refreshes.Load(); got != 1 {
		t.Fatalf("refreshes = %d, want exactly one under the session file lock", got)
	}
	if tokens[0] != tokens[1] {
		t.Fatal("the second process must use the token the first one stored")
	}
}

func TestTokenSource_InvalidGrantClearsTheTokenAndKeepsTheURL(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	p := f.signedIn(t, "prod")
	p.Token.Refresh = "spent"
	p.ExpiresAt = time.Now().Add(-time.Minute)
	writeRemoteProfile(t, f.store.path, "prod", p)

	_, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL)
	if !IsSessionExpiredError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v (exit %d), want SessionExpiredError exiting 2", err, ExitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "auth login --url "+f.app.URL+" --profile prod") {
		t.Fatalf("the hint must name the URL and profile: %v", err)
	}
	after := f.profile(t, "prod")
	if after.Token != nil || after.URL != f.app.URL {
		t.Fatalf("profile = %+v, want the token cleared and the URL kept", after)
	}
	if _, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL); !IsSessionExpiredError(err) {
		t.Fatalf("an expired profile keeps answering SessionExpiredError, got %v", err)
	}
}

func TestTokenSource_IssuerOutageKeepsTheTokens(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "prod")
	f.expire(t, "prod")
	before := f.profile(t, "prod")
	f.idp.TokenStatus = http.StatusServiceUnavailable

	_, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL)
	if err == nil || IsSessionExpiredError(err) || ExitCodeFor(err) != ExitGeneral {
		t.Fatalf("err = %v (exit %d), want a general error that signs no one out", err, ExitCodeFor(err))
	}
	if after := f.profile(t, "prod"); after.Token == nil || after.Token.Refresh != before.Token.Refresh {
		t.Fatalf("an outage dropped the tokens: %+v", after.Token)
	}
}

func TestTokenSource_KeepsARotatedTokenFromAFailedRefresh(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "prod")
	f.expire(t, "prod")
	before := f.profile(t, "prod")
	f.traffic.mangle.Store(true)

	if _, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL); err == nil {
		t.Fatal("a token response with a non-Bearer type must fail")
	}
	mid := f.profile(t, "prod")
	if mid.Token == nil || mid.Token.Refresh == before.Token.Refresh {
		t.Fatal("RFC 9700 Section 4.14.2: the issuer rotated the refresh token, so the new one must be stored")
	}

	f.traffic.mangle.Store(false)
	if _, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL); err != nil {
		t.Fatalf("the stored rotated token must still refresh: %v", err)
	}
}

func TestTokenSource_SendsAProfileOnlyToItsOwnServer(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "prod")
	writeRemoteProfile(t, f.store.path, "local", profile{Principal: principalView{Email: "local@b.co"}})

	if _, err := f.source("prod", time.Now).accessToken(t.Context(), "https://other.example"); !IsProfileHostMismatchError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v, want ProfileHostMismatchError exiting 2", err)
	}
	for _, name := range []string{"local", "nobody"} {
		_, err := f.source(name, time.Now).accessToken(t.Context(), f.app.URL)
		if !IsNotSignedInError(err) || !strings.Contains(err.Error(), "auth login --url "+f.app.URL) {
			t.Fatalf("profile %s: err = %v, want NotSignedInError hinting auth login --url", name, err)
		}
	}
}

func TestLogout_RevokesTheRefreshTokenThenRemovesTheProfile(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	p := f.signedIn(t, "prod")
	f.out.Reset()

	if err := logout(t.Context(), f.store, "prod", f.client, &f.out, &f.errOut); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if f.idp.Revocations() != 1 {
		t.Fatalf("revocations = %d, want 1 (RFC 7009)", f.idp.Revocations())
	}
	if got, _ := f.store.load("prod"); got != nil {
		t.Fatal("logout left the profile behind")
	}
	if !strings.Contains(f.out.String(), "Signed out of "+f.app.URL+" (profile prod)") {
		t.Fatalf("stdout = %q", f.out.String())
	}

	p.ExpiresAt = time.Now().Add(-time.Minute)
	writeRemoteProfile(t, f.store.path, "prod", p)
	if _, err := f.source("prod", time.Now).accessToken(t.Context(), f.app.URL); !IsSessionExpiredError(err) {
		t.Fatalf("the revoked refresh token still refreshed: %v", err)
	}
}

func TestLogout_StillRemovesTheProfileWhenRevocationFails(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "prod")
	f.traffic.revokeErr.Store(true)

	if err := logout(t.Context(), f.store, "prod", f.client, &f.out, &f.errOut); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(f.errOut.String(), "warn: the refresh token was not revoked") {
		t.Fatalf("stderr = %q, want a warning", f.errOut.String())
	}
	if got, _ := f.store.load("prod"); got != nil {
		t.Fatal("a failed revocation must not keep the profile")
	}
}

func TestLogout_LocalAndMissingProfiles(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	writeRemoteProfile(t, f.store.path, "local", profile{Principal: principalView{Email: "local@b.co"}})

	if err := logout(t.Context(), f.store, "local", f.client, &f.out, &f.errOut); err != nil {
		t.Fatal(err)
	}
	if err := logout(t.Context(), f.store, "nobody", f.client, &f.out, &f.errOut); err != nil {
		t.Fatal(err)
	}
	if f.idp.Revocations() != 0 {
		t.Fatal("a local profile holds no token to revoke")
	}
	if !strings.Contains(f.out.String(), "Signed out (profile local)") || !strings.Contains(f.out.String(), "not signed in (profile nobody)") {
		t.Fatalf("stdout = %q", f.out.String())
	}
}

func TestRemoteLogin_RefusesAPlainHTTPIssuer(t *testing.T) {
	for _, issuer := range []string{"http://issuer.example", "http://localhost:1"} {
		t.Run(issuer, func(t *testing.T) {
			var resource string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"resource":"`+resource+`","authorization_servers":["`+issuer+`"],"cli_client_id":"c"}`)
			}))
			t.Cleanup(srv.Close)
			resource = srv.URL + apiPath
			f := newRemoteFixture(t, testCLIClient)
			err := f.login(t, srv.URL, "prod")
			if !IsInsecureURLError(err) {
				t.Fatalf("err = %v, want InsecureURLError: a plain-http issuer must be a loopback IP literal", err)
			}
			if len(f.browser.urls) != 0 {
				t.Fatal("the browser opened for an insecure issuer")
			}
		})
	}
}

func TestCheckServerURL(t *testing.T) {
	for raw, want := range map[string]func(error) bool{
		"https://app.example.com":         func(err error) bool { return err == nil },
		"http://localhost:8080":           func(err error) bool { return err == nil },
		"http://127.0.0.1:8080/base":      func(err error) bool { return err == nil },
		"http://app.example.com":          IsInsecureURLError,
		"https://user:pw@app.example.com": IsMalformedURLError,
		"https://app.example.com?x=1":     IsMalformedURLError,
		"https://app.example.com#frag":    IsMalformedURLError,
		"https://app.example.com?":        IsMalformedURLError,
		"ftp://app.example.com":           IsMalformedURLError,
		"app.example.com":                 IsMalformedURLError,
	} {
		if err := checkServerURL(raw); !want(err) {
			t.Errorf("checkServerURL(%q) = %v", raw, err)
		}
	}
	if err := checkIssuerURL("http://127.0.0.1:9000", "https://app.example.com"); !IsInsecureURLError(err) {
		t.Errorf("a loopback issuer for a public app must be refused, got %v", err)
	}
	if err := checkIssuerURL("http://127.0.0.1:9000", "http://localhost:8080"); err != nil {
		t.Errorf("a loopback issuer for a loopback app is the dev setup, got %v", err)
	}
}

func TestRemoteLogin_SignsInAgainRevokingTheOldRefreshToken(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	first := f.signedIn(t, "prod")
	second := f.signedIn(t, "prod")

	if f.idp.Revocations() != 1 || first.Token.Refresh == second.Token.Refresh {
		t.Fatalf("revocations = %d; the replaced refresh token must be revoked", f.idp.Revocations())
	}
	if !strings.Contains(f.errOut.String(), "Signing in through "+f.idp.Issuer()) {
		t.Fatalf("stderr = %q, want the issuer named before the browser opens", f.errOut.String())
	}
}

func TestTokenSource_InterruptDuringRefreshKeepsTheRotatedToken(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "prod")
	f.expire(t, "prod")
	before := f.profile(t, "prod")
	f.traffic.delay = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := f.source("prod", time.Now).accessToken(ctx, f.app.URL); err != nil {
		t.Fatalf("a refresh already sent must finish and be stored: %v", err)
	}
	if after := f.profile(t, "prod"); after.Token.Refresh == before.Token.Refresh {
		t.Fatal("the rotated refresh token was lost to the interrupt")
	}
}

func TestTokenSource_NoExpiryAndNoRefreshTokenIsUsable(t *testing.T) {
	s := tokenSource{now: time.Now}
	if !s.fresh(profile{Token: &profileToken{Access: "at"}}) {
		t.Fatal("without expires_in or a refresh token the access token is used until the server refuses it")
	}
	if s.fresh(profile{Token: &profileToken{Access: "at", Refresh: "rt"}}) {
		t.Fatal("without expires_in but with a refresh token, every call refreshes")
	}
}

func TestResolveRemote_ExplicitTokenIgnoresABrokenSessionFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := invocation{cfg: config.Defaults(), explicitConfig: true, store: profileStore{path: path}, profile: defaultProfile, token: "tok"}
	in.cfg.HTTP.BaseURL = "https://cfg.example"
	for _, explicit := range []string{"", "https://flag.example"} {
		in.url = explicit
		r, err := resolveRemote(t.Context(), in, http.DefaultClient)
		if err != nil || r.token != "tok" || r.fromProfile {
			t.Fatalf("url %q: remote = %+v, %v", explicit, r, err)
		}
	}
	in.token = ""
	if _, err := resolveRemote(t.Context(), in, http.DefaultClient); !IsSessionFileError(err) {
		t.Fatalf("without a token the broken file must surface, got %v", err)
	}
}

func TestResolveRemote_RefusesABearerOverPlainHTTP(t *testing.T) {
	in := invocation{cfg: config.Defaults(), store: profileStore{path: filepath.Join(t.TempDir(), "session.json")}, profile: defaultProfile}
	for _, tc := range []struct{ url, token string }{
		{"http://app.example", "tok"},
		{"http://app.example", ""},
	} {
		in.url, in.token = tc.url, tc.token
		if _, err := resolveRemote(t.Context(), in, http.DefaultClient); !IsInsecureURLError(err) {
			t.Fatalf("RFC 6750 Section 5.3: %+v: err = %v, want an insecure URL error", tc, err)
		}
	}
	in.url, in.token = "http://127.0.0.1:5180", "tok"
	if _, err := resolveRemote(t.Context(), in, http.DefaultClient); err != nil {
		t.Fatalf("loopback stays allowed for local development: %v", err)
	}
}

// SECURITY: RFC 7009: a local sign-in that overwrites a remote profile revokes the refresh token it replaces.
func TestReplaceProfile_LocalOverRemoteRevokesTheRefreshToken(t *testing.T) {
	f := newRemoteFixture(t, testCLIClient)
	f.signedIn(t, "ops")

	local := profile{Principal: principalView{Email: testEmail, UserID: "usr_local"}}
	if err := replaceProfile(t.Context(), f.store, f.client, &f.errOut, "ops", local); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if f.idp.Revocations() != 1 {
		t.Fatalf("revocations = %d, want the replaced refresh token revoked", f.idp.Revocations())
	}
	got, err := f.store.load("ops")
	if err != nil || got == nil || got.URL != "" || got.Token != nil {
		t.Fatalf("profile = %+v, %v; want the local profile", got, err)
	}
}

// SECURITY: RFC 7009: an `auth login` that overwrites a remote profile revokes the refresh token it replaces.
func TestLocalLogin_OverARemoteProfileRevokesItsRefreshToken(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	f := newRemoteFixture(t, testCLIClient)
	f.store = profileStore{path: sessPath}
	f.signedIn(t, "ops")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("hunter2\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&f.out)
	cmd.SetErr(&f.errOut)
	bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return boot.BootServer(ctx, cfg)
	}
	if err := localLogin(cmd, bootFn, cfg, f.store, "ops", loginOpts{email: "root@example.com", passwordStdin: true}, f.client); err != nil {
		t.Fatalf("local login: %v (stderr %q)", err, f.errOut.String())
	}
	if f.idp.Revocations() != 1 {
		t.Fatalf("revocations = %d, want the replaced refresh token revoked", f.idp.Revocations())
	}
	got, err := f.store.load("ops")
	if err != nil || got == nil || got.URL != "" || got.Token != nil || got.Principal.Email != "root@example.com" {
		t.Fatalf("profile = %+v, %v; want the local profile", got, err)
	}
}
