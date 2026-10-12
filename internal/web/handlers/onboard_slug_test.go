package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web/handlers"
)

var (
	generatedSlugShape = regexp.MustCompile(`^[a-z]{3,8}-[a-z]{3,8}-[1-9][0-9]{3}$`)
	inputTag           = regexp.MustCompile(`<input[^>]*>`)
	valueAttr          = regexp.MustCompile(`\svalue="([^"]*)"`)
)

func newOnboardMux(t *testing.T, f *handlerFixture) *http.ServeMux {
	t.Helper()
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, nil, "")
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

type slugInput struct {
	value    string
	readonly bool
}

func slugInputs(t *testing.T, body, name string) []slugInput {
	t.Helper()
	var out []slugInput
	for _, tag := range inputTag.FindAllString(body, -1) {
		if !strings.Contains(tag, ` name="`+name+`"`) {
			continue
		}
		in := slugInput{readonly: strings.Contains(tag, " readonly")}
		if m := valueAttr.FindStringSubmatch(tag); m != nil {
			in.value = m[1]
		}
		out = append(out, in)
	}
	require.NotEmpty(t, out, "the page must render a %s input", name)
	return out
}

func assertEditControl(t *testing.T, body, name string) {
	t.Helper()
	assert.Contains(t, body, `data-slug-edit="onb-`+name+`"`, "%s must carry an Edit control", name)
}

func postOnboardLocal(t *testing.T, mux *http.ServeMux, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/onboard/local", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func localForm(orgSlug, projectSlug string) url.Values {
	return url.Values{
		"email": {"admin@example.com"}, "name": {"Admin"}, "password": {"secret-password"},
		"org_slug": {orgSlug}, "org_name": {"Acme"},
		"project_slug": {projectSlug}, "project_name": {"Main"},
	}
}

func onboardedSlugs(t *testing.T, f *handlerFixture) (string, string) {
	t.Helper()
	o, err := f.Orgs.SystemOrg(context.Background())
	require.NoError(t, err, "onboarding must leave a system org behind")
	ps, err := f.Projects.List(tenant.Into(context.Background(), tenant.Context{OrgID: o.ID, UserID: o.OwnerID}), o.ID)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	require.True(t, ps[0].System)
	return o.Slug, ps[0].Slug
}

func TestOnboard_GetPrefillsGeneratedReadonlySlugs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := httptest.NewRecorder()
	newOnboardMux(t, f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	for _, name := range []string{"org_slug", "project_slug"} {
		for _, in := range slugInputs(t, body, name) {
			assert.Regexp(t, generatedSlugShape, in.value, "an unset %s must prefill a generated slug", name)
			assert.True(t, in.readonly, "%s must render read-only until Edit is chosen", name)
		}
		assertEditControl(t, body, name)
	}
	assert.Contains(t, body, "data-slug-edit", "the Edit script must be on the page")
	assert.NotContains(t, body, "onclick", "CSP forbids inline handlers")
}

func TestOnboard_GetPrefillsThePinnedSlugs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Tenant.SingletonOrg.Slug = "acme-pinned"
	f.Cfg.Tenant.PersonalProjectSlug = "web-pinned"
	rec := httptest.NewRecorder()
	newOnboardMux(t, f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	for _, in := range slugInputs(t, rec.Body.String(), "org_slug") {
		assert.Equal(t, "acme-pinned", in.value)
		assert.True(t, in.readonly)
	}
	for _, in := range slugInputs(t, rec.Body.String(), "project_slug") {
		assert.Equal(t, "web-pinned", in.value)
		assert.True(t, in.readonly)
	}
}

func TestOnboard_GetOIDCCompletePrefillsReadonlySlugs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceOIDC})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	newOnboardMux(t, f).ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/onboard/complete", "", session.Principal{UserID: u.ID, Email: u.Email}))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	for _, name := range []string{"org_slug", "project_slug"} {
		ins := slugInputs(t, body, name)
		require.Len(t, ins, 1)
		assert.Regexp(t, generatedSlugShape, ins[0].value)
		assert.True(t, ins[0].readonly)
		assertEditControl(t, body, name)
	}
}

func TestOnboard_PostLocalUntouchedGeneratedSlugsAreUsed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mux := newOnboardMux(t, f)
	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/onboard", nil))
	orgSlug := slugInputs(t, get.Body.String(), "org_slug")[0].value
	projectSlug := slugInputs(t, get.Body.String(), "project_slug")[0].value

	rec := postOnboardLocal(t, mux, localForm(orgSlug, projectSlug))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	gotOrg, gotProject := onboardedSlugs(t, f)
	assert.Equal(t, orgSlug, gotOrg)
	assert.Equal(t, projectSlug, gotProject)
}

func TestOnboard_PostLocalEditedSlugsAreUsed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := postOnboardLocal(t, newOnboardMux(t, f), localForm("acme-hq", "main-site"))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	gotOrg, gotProject := onboardedSlugs(t, f)
	assert.Equal(t, "acme-hq", gotOrg)
	assert.Equal(t, "main-site", gotProject)
}

func TestOnboard_PostLocalBlankSlugsAreGenerated(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := postOnboardLocal(t, newOnboardMux(t, f), localForm("", ""))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	gotOrg, gotProject := onboardedSlugs(t, f)
	assert.Regexp(t, generatedSlugShape, gotOrg)
	assert.Regexp(t, generatedSlugShape, gotProject)
}

func TestOnboard_PostLocalBlankSlugsFallBackToThePinnedOnes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Tenant.SingletonOrg.Slug = "acme-pinned"
	f.Cfg.Tenant.PersonalProjectSlug = "web-pinned"
	rec := postOnboardLocal(t, newOnboardMux(t, f), localForm("", ""))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	gotOrg, gotProject := onboardedSlugs(t, f)
	assert.Equal(t, "acme-pinned", gotOrg)
	assert.Equal(t, "web-pinned", gotProject)
}

func TestOnboard_PostLocalInvalidEditedSlugShowsTheFieldError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		field, orgSlug, projectSlug string
	}{
		{"org_slug", "Not A Slug!", "main"},
		{"project_slug", "acme", "Not A Slug!"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			rec := postOnboardLocal(t, newOnboardMux(t, f), localForm(tc.orgSlug, tc.projectSlug))
			require.Equal(t, http.StatusOK, rec.Code)
			body := rec.Body.String()

			ins := slugInputs(t, body, tc.field)
			assert.Equal(t, "Not A Slug!", ins[0].value, "the rejected value must come back for correction")
			assert.False(t, ins[0].readonly, "a slug with an error must render editable")
			assert.Contains(t, body, "onboard.error.slug_invalid", "the field error must be a translated key, not raw Go error text")
			assert.NotContains(t, body, "must be lowercase alphanumeric with dashes")
			assert.Zero(t, f.UserStore.Len(), "a rejected slug must not leave a half-onboarded admin behind")
			_, err := f.Orgs.SystemOrg(context.Background())
			assert.True(t, org.IsNotFoundError(err), "no org may be created for a rejected slug")
		})
	}
}

func TestOnboard_PostOIDCCompleteBlankSlugsAreGenerated(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceOIDC})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	body := "org_slug=&org_name=Acme&project_slug=&project_name=Main"
	newOnboardMux(t, f).ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/onboard/complete", body, session.Principal{UserID: u.ID, Email: u.Email}))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	gotOrg, gotProject := onboardedSlugs(t, f)
	assert.Regexp(t, generatedSlugShape, gotOrg)
	assert.Regexp(t, generatedSlugShape, gotProject)
}

func TestOnboard_PostLocalInvalidNamesAreCaughtBeforeTheAdminIsCreated(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 300)
	for _, tc := range []struct {
		field, key string
		form       url.Values
	}{
		{"org_name", "onboard.error.org_name_invalid", func() url.Values { f := localForm("acme", "main"); f.Set("org_name", long); return f }()},
		{"project_name", "onboard.error.project_name_invalid", func() url.Values { f := localForm("acme", "main"); f.Set("project_name", long); return f }()},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			rec := postOnboardLocal(t, newOnboardMux(t, f), tc.form)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.key)
			assert.Zero(t, f.UserStore.Len(), "a bad name must be refused before the admin exists, or the resubmit hits 'user exists'")
		})
	}
}

func TestOnboard_PostOnAStaleReplicaIsRefusedOnceTheDBSaysOnboarded(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"/onboard/local", "/onboard/complete"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			first, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "first@example.com", Name: "First", Source: user.SourceLocal, Password: "secret-password"})
			require.NoError(t, err)
			_, err = f.Onboards.Complete(t.Context(), first.ID, onboard.MethodWebOnboard)
			require.NoError(t, err)
			oidc, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "late@example.com", Name: "Late", Source: user.SourceOIDC})
			require.NoError(t, err)
			users := f.UserStore.Len()

			mux := newOnboardMux(t, f)
			rec := httptest.NewRecorder()
			if route == "/onboard/local" {
				rec = postOnboardLocal(t, mux, localForm("acme", "web"))
			} else {
				body := "org_slug=acme&org_name=Acme&project_slug=web&project_name=Web"
				mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, route, body, session.Principal{UserID: oidc.ID, Email: oidc.Email}))
			}

			require.Equal(t, http.StatusSeeOther, rec.Code, "a replica whose in-process gate is stale must re-read the DB and refuse")
			assert.Equal(t, "/", rec.Header().Get("Location"))
			assert.Equal(t, users, f.UserStore.Len(), "no admin may be created after onboarding completed elsewhere")
			_, err = f.Orgs.SystemOrg(context.Background())
			assert.True(t, org.IsNotFoundError(err), "no org may be bootstrapped by a stale replica")
			after, err := f.UserStore.ByID(context.Background(), oidc.ID)
			require.NoError(t, err)
			assert.False(t, after.IsAdmin, "a stale replica must not promote the OIDC user")
		})
	}
}
