package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/altalune-id/oidcrp"
	"github.com/altalune-id/oidcrp/oidcrptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/handlers"
)

const (
	oidcStateCookie = "opensheet_oidc_state"
	lastUserCookie  = "opensheet_last_user"
)

type oidcHarness struct {
	f      *handlerFixture
	idp    *oidcrptest.IDP
	mux    *http.ServeMux
	secret []byte
	tr     *i18n.Translator
}

func newOIDCHarness(t *testing.T) *oidcHarness {
	t.Helper()
	f := newFixture(t)
	idp := oidcrptest.New(t)
	f.Cfg.OIDC.Issuer = idp.Issuer()
	f.Cfg.OIDC.ClientID = "web-client"
	rp := oidcrp.New(oidcrp.Config{
		Issuer: idp.Issuer(), ClientID: "web-client", ClientSecret: "web-secret",
		RedirectURI: "http://localhost/oauth/callback", Scopes: []string{"openid", "email", "profile"}, HTTPClient: idp.Client(),
	})
	login := auth.NewOIDCLogin(
		func(ctx context.Context, c auth.EnsureClaims) (*auth.UserRef, bool, error) {
			u, err := f.Users.EnsureFromOIDC(ctx, user.Claims(c))
			if err != nil {
				return nil, false, err
			}
			return &auth.UserRef{ID: u.ID, Email: u.Email, Name: u.Name, Source: u.Source}, false, nil
		},
		nil, discardLogger(), passthroughUnexpected(),
	)
	authSvc := auth.NewService(nil, login, discardLogger(), passthroughUnexpected())
	secret := []byte("resolved-state-secret-32-bytes!!")
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, rp, nil).WithStateSecret(secret)
	mux := http.NewServeMux()
	h.Register(mux)
	return &oidcHarness{f: f, idp: idp, mux: mux, secret: secret}
}

func (o *oidcHarness) do(r *http.Request) *httptest.ResponseRecorder {
	if o.tr != nil {
		r = r.WithContext(i18n.TranslatorInto(r.Context(), o.tr))
	}
	rec := httptest.NewRecorder()
	o.mux.ServeHTTP(rec, r)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (o *oidcHarness) start(t *testing.T, query string, cookies ...*http.Cookie) (authURL string, state *http.Cookie) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/login/oidc"+query, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := o.do(r)
	require.Equal(t, http.StatusFound, rec.Code, "body=%s", rec.Body.String())
	state = cookieNamed(rec, oidcStateCookie)
	require.NotNil(t, state, "the start must set the signed state cookie")
	assert.True(t, state.HttpOnly)
	return rec.Header().Get("Location"), state
}

func (o *oidcHarness) callback(q url.Values, state *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?"+q.Encode(), nil)
	if state != nil {
		r.AddCookie(state)
	}
	return o.do(r)
}

func TestOIDC_SignInWritesTheSessionAndHonoursReturnTo(t *testing.T) {
	t.Parallel()
	o := newOIDCHarness(t)

	authURL, state := o.start(t, "?return_to=/projects")
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	assert.Equal(t, "select_account", u.Query().Get("prompt"), "with no account in mind the picker shows")
	assert.Equal(t, "S256", u.Query().Get("code_challenge_method"))

	rec := o.callback(o.idp.Authorize(authURL), state)
	require.Equal(t, http.StatusSeeOther, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "/projects", rec.Header().Get("Location"))

	sess := cookieNamed(rec, web.SessionCookieName)
	require.NotNil(t, sess, "a successful sign-in sets the session cookie")
	sid, err := web.VerifyCookie(o.f.Deps.SecretBytes(), sess.Value)
	require.NoError(t, err)
	p, ok, err := o.f.Sessions.Load(t.Context(), sid)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, o.idp.User.Email, p.Email)
	assert.Equal(t, o.idp.Issuer(), p.IDPIssuer)
	assert.NotEmpty(t, p.IDToken, "the ID token is kept as the logout hint")

	last := cookieNamed(rec, lastUserCookie)
	require.NotNil(t, last)
	email, err := web.VerifyCookie(o.secret, last.Value)
	require.NoError(t, err, "the last-user cookie is signed with the resolved state secret")
	assert.Equal(t, o.idp.User.Email, email)
	cleared := cookieNamed(rec, oidcStateCookie)
	require.NotNil(t, cleared)
	assert.Negative(t, cleared.MaxAge, "the state cookie is single-use")
}

func TestOIDC_LastUserIsTheLoginHintUnlessSwitching(t *testing.T) {
	t.Parallel()
	o := newOIDCHarness(t)
	last := &http.Cookie{Name: lastUserCookie, Value: web.SignCookie(o.secret, " Ana@Gmail.com ")}

	authURL, _ := o.start(t, "", last)
	q := mustQuery(t, authURL)
	assert.Equal(t, "ana@gmail.com", q.Get("login_hint"))
	assert.Empty(t, q.Get("prompt"))

	authURL, _ = o.start(t, "?switch=1", last)
	q = mustQuery(t, authURL)
	assert.Empty(t, q.Get("login_hint"))
	assert.Equal(t, "select_account", q.Get("prompt"))
}

func TestOIDC_StateCookieIsSignedWithTheResolvedSecret(t *testing.T) {
	t.Parallel()
	o := newOIDCHarness(t)
	authURL, state := o.start(t, "")

	_, err := web.VerifyCookie(o.secret, state.Value)
	require.NoError(t, err, "the state cookie must verify with the resolved secret")
	_, err = web.VerifyCookie([]byte(o.f.Cfg.HTTP.StateSecret), state.Value)
	require.Error(t, err, "the state cookie must not be signed with the raw config string")

	rec := o.callback(o.idp.Authorize(authURL), state)
	require.Equal(t, http.StatusSeeOther, rec.Code, "body=%s", rec.Body.String())
}

func TestOIDC_ExpiredStateCookieIsRefused(t *testing.T) {
	t.Parallel()
	o := newOIDCHarness(t)
	authURL, state := o.start(t, "")

	value, err := web.VerifyCookie(o.secret, state.Value)
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(value)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	payload["e"] = time.Now().Add(-time.Minute).Unix()
	raw, err = json.Marshal(payload)
	require.NoError(t, err)
	expired := &http.Cookie{Name: oidcStateCookie, Value: web.SignCookie(o.secret, base64.RawURLEncoding.EncodeToString(raw))}

	rec := o.callback(o.idp.Authorize(authURL), expired)
	require.Equal(t, http.StatusBadRequest, rec.Code, "an expired state cookie must be refused even with a valid signature")
	assert.Nil(t, cookieNamed(rec, web.SessionCookieName))
}

func TestOIDC_HostileReturnToStaysUnderBasePath(t *testing.T) {
	t.Parallel()
	for _, returnTo := range []string{"//evil.example", "https://evil.example", `\\evil.example`, `/\evil`, "javascript:alert(1)"} {
		for _, basePath := range []string{"", "/app"} {
			t.Run(basePath+" "+returnTo, func(t *testing.T) {
				o := newOIDCHarness(t)
				o.f.Cfg.HTTP.BasePath = basePath
				authURL, state := o.start(t, "?return_to="+url.QueryEscape(returnTo))
				rec := o.callback(o.idp.Authorize(authURL), state)
				require.Equal(t, http.StatusSeeOther, rec.Code, "body=%s", rec.Body.String())
				assert.Equal(t, basePath+"/", rec.Header().Get("Location"), "return_to %q escaped the app", returnTo)
			})
		}
	}
}

func TestOIDC_ForgedLastUserCookieIsIgnored(t *testing.T) {
	t.Parallel()
	o := newOIDCHarness(t)
	authURL, _ := o.start(t, "", &http.Cookie{Name: lastUserCookie, Value: "ana@gmail.com"})
	q := mustQuery(t, authURL)
	assert.Empty(t, q.Get("login_hint"), "an unsigned last-user cookie must not become a hint")
	assert.Equal(t, "select_account", q.Get("prompt"))
}

func TestOIDC_StartRefusesWithoutAStateSecret(t *testing.T) {
	t.Parallel()
	o := newOIDCHarness(t)
	h := handlers.NewAuthHandler(o.f.Deps, nil, o.f.Users, o.f.Orgs, o.f.Projects, oidcrp.New(oidcrp.Config{Issuer: o.idp.Issuer(), HTTPClient: o.idp.Client()}), nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login/oidc", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Nil(t, cookieNamed(rec, oidcStateCookie), "no state cookie is ever signed with an empty key")
}

func TestOIDC_CallbackFailuresNeverShowUpstreamText(t *testing.T) {
	t.Parallel()
	const upstream = "upstream-secret-text"

	tests := []struct {
		name     string
		run      func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder
		status   int
		location string
		key      string
	}{
		{
			name: "no state cookie",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, _ := o.start(t, "")
				return o.callback(o.idp.Authorize(authURL), nil)
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "a forged state cookie",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, _ := o.start(t, "")
				return o.callback(o.idp.Authorize(authURL), &http.Cookie{Name: oidcStateCookie, Value: "forged|sig"})
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "a state that is not this attempt's",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				q := o.idp.Authorize(authURL)
				q.Set("state", "someone-elses")
				return o.callback(q, state)
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "an iss that is not the issuer",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				q := o.idp.Authorize(authURL)
				q.Set("iss", "https://evil.example.com")
				return o.callback(q, state)
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "an upstream error with a description",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				q := o.idp.Authorize(authURL)
				q.Del("code")
				q.Set("error", "server_error")
				q.Set("error_description", upstream)
				return o.callback(q, state)
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "a nonce that is not this attempt's",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				o.idp.Claims = map[string]any{"nonce": "replayed"}
				return o.callback(o.idp.Authorize(authURL), state)
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "the issuer refuses the code",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				q := o.idp.Authorize(authURL)
				q.Set("code", "unknown")
				return o.callback(q, state)
			},
			status: http.StatusBadRequest,
			key:    "errors.signin.expired",
		},
		{
			name: "the issuer is failing",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				o.idp.TokenStatus = http.StatusInternalServerError
				return o.callback(o.idp.Authorize(authURL), state)
			},
			status: http.StatusBadGateway,
			key:    "errors.signin.unavailable",
		},
		{
			name: "an unverified email",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				o.idp.User.EmailVerified = false
				return o.callback(o.idp.Authorize(authURL), state)
			},
			status: http.StatusForbidden,
			key:    "errors.signin.email_unverified",
		},
		{
			name: "an email linked to another subject at the issuer",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				_, err := o.f.Users.EnsureFromOIDC(t.Context(), user.Claims{
					Issuer: o.idp.Issuer(), Subject: "someone-else", Email: o.idp.User.Email, EmailVerified: true, Name: "Other",
				})
				require.NoError(t, err)
				authURL, state := o.start(t, "")
				return o.callback(o.idp.Authorize(authURL), state)
			},
			status: http.StatusForbidden,
			key:    "errors.signin.identity_conflict",
		},
		{
			name: "a sign-in the app refuses",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				o.idp.User.Email = ""
				return o.callback(o.idp.Authorize(authURL), state)
			},
			status: http.StatusForbidden,
			key:    "errors.signin.not_permitted",
		},
		{
			name: "the user store is failing",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				o.f.UserStore.ByIDPErr = errors.New("database unreachable")
				return o.callback(o.idp.Authorize(authURL), state)
			},
			status: http.StatusInternalServerError,
			key:    "errors.signin.unavailable",
		},
		{
			name: "the person cancelled",
			run: func(t *testing.T, o *oidcHarness) *httptest.ResponseRecorder {
				authURL, state := o.start(t, "")
				return o.callback(o.idp.Deny(authURL), state)
			},
			status:   http.StatusSeeOther,
			location: "/login",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := newOIDCHarness(t)
			o.tr = i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
			rec := tt.run(t, o)
			require.Equal(t, tt.status, rec.Code, "body=%s", rec.Body.String())
			if tt.location != "" {
				assert.Equal(t, tt.location, rec.Header().Get("Location"))
			}
			for _, leak := range []string{upstream, "oidcrp:", "user:", "auth: oidc"} {
				assert.NotContains(t, rec.Body.String(), leak, "the page must name a reason, never an internal error")
			}
			assert.Nil(t, cookieNamed(rec, web.SessionCookieName), "a failed sign-in must not start a session")
			if tt.key != "" {
				assert.Contains(t, rec.Body.String(), o.tr.T(tt.key+".body"), "the page must render %s", tt.key)
			}
		})
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Query()
}

func TestSignInRefusalIsAnExplicitList(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want bool
	}{
		{codes.PermissionDenied, true},
		{codes.FailedPrecondition, true},
		{codes.InvalidArgument, true},
		{codes.Unauthenticated, true},
		{codes.Unavailable, false},
		{codes.Internal, false},
		{codes.NotFound, false},
	} {
		err := apperror.New("TST001", "x", tc.code, &apperrorv1.ErrorDetail{Code: "TST001"})
		assert.Equal(t, tc.want, handlers.IsSignInRefusal(err), "%v", tc.code)
	}
	assert.False(t, handlers.IsSignInRefusal(errors.New("connection refused")), "an untyped error is an outage")
}
