package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web/handlers"
)

type projectCatalog struct{ svc *project.Service }

func (c projectCatalog) ProjectIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	list, err := c.svc.List(tenant.WithOrg(ctx, orgID), orgID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

type apikeyWebFixture struct {
	*handlerFixture
	Keys   *apikey.Service
	Store  *fakes.APIKey
	Mux    *http.ServeMux
	owner  uuid.UUID
	member uuid.UUID
	org    uuid.UUID
	alpha  *project.Project
	beta   *project.Project
}

func newAPIKeyWebFixture(t *testing.T) *apikeyWebFixture {
	t.Helper()
	f := newFixture(t)
	owner, member := uuid.New(), uuid.New()
	o := f.seedOrg(t, "acme", owner)
	m, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, f.OrgStore.SaveMembership(context.Background(), m))
	alpha, err := f.Projects.Create(setTenant(context.Background(), o.ID, owner), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	beta, err := f.Projects.Create(setTenant(context.Background(), o.ID, owner), o.ID, "beta", "Beta")
	require.NoError(t, err)

	store := fakes.NewAPIKey()
	keys := apikey.NewService(store, apikey.Scheme{}, f.Orgs, projectCatalog{svc: f.Projects}, discardLogger(), passthroughUnexpected())
	mux := http.NewServeMux()
	handlers.NewAPIKeyHandler(f.Deps, f.Projects, keys).Register(mux)
	return &apikeyWebFixture{handlerFixture: f, Keys: keys, Store: store, Mux: mux, owner: owner, member: member, org: o.ID, alpha: alpha, beta: beta}
}

func (x *apikeyWebFixture) as(t *testing.T, userID uuid.UUID, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := x.authedRequest(t, method, target, form.Encode(), session.Principal{UserID: userID, ActiveOrgID: x.org})
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	return rec
}

func (x *apikeyWebFixture) orgKeys(t *testing.T) []*apikey.APIKey {
	t.Helper()
	keys, err := x.Keys.ListOrg(setTenant(context.Background(), x.org, x.owner))
	require.NoError(t, err)
	return keys
}

func TestOrgAPIKeys_OwnerMintsASelectedProjectsKey(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	rec := x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys", url.Values{
		"name": {"ci"}, "scopes": {authn.ScopeSheetsRead}, "grant": {"selected"}, "project_ids": {x.alpha.ID.String()}, "expires_in": {"30"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	keys := x.orgKeys(t)
	require.Len(t, keys, 1)
	assert.Equal(t, []uuid.UUID{x.alpha.ID}, keys[0].ProjectIDs)
	assert.Contains(t, rec.Body.String(), "…"+keys[0].SecretHint, "the list must show only the secret suffix")
	assert.Contains(t, rec.Body.String(), "Alpha")
}

func TestOrgAPIKeys_OwnerPromotesAKey(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys", url.Values{
		"name": {"ci"}, "grant": {"selected"}, "project_ids": {x.alpha.ID.String()}, "expires_in": {"30"},
	})
	id := x.orgKeys(t)[0].ID.String()

	rec := x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys/"+id+"/projects", url.Values{"project_ids": {x.beta.ID.String()}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.ElementsMatch(t, []uuid.UUID{x.alpha.ID, x.beta.ID}, x.orgKeys(t)[0].ProjectIDs)

	rec = x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys/"+id+"/all-projects", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, x.orgKeys(t)[0].AllProjects)
	assert.NotContains(t, rec.Body.String(), "/all-projects", "an all-projects key offers no further promotion")
}

func TestOrgAPIKeys_EmptySelectionIsRefused(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	rec := x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys", url.Values{"name": {"ci"}, "grant": {"selected"}, "expires_in": {"30"}})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "apikey.error_empty_grant")
	assert.Empty(t, x.orgKeys(t))
}

// SECURITY: a member sees the lists read-only and every write is refused by the service, not only hidden by the page.
func TestAPIKeys_MemberIsReadOnly(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys", url.Values{"name": {"ci"}, "grant": {"all"}, "expires_in": {"30"}})
	id := x.orgKeys(t)[0].ID.String()

	page := x.as(t, x.member, http.MethodGet, "/orgs/acme/apikeys", nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), "apikey.read_only")
	assert.NotContains(t, page.Body.String(), `name="name"`, "a member must not get the mint form")
	assert.NotContains(t, page.Body.String(), "/revoke", "a member must not get a revoke button")

	for name, target := range map[string]string{
		"mint org key":     "/orgs/acme/apikeys",
		"revoke org key":   "/orgs/acme/apikeys/" + id + "/revoke",
		"mint project key": "/orgs/acme/projects/alpha/apikeys",
	} {
		t.Run(name, func(t *testing.T) {
			rec := x.as(t, x.member, http.MethodPost, target, url.Values{"name": {"sneaky"}, "grant": {"all"}, "expires_in": {"30"}})
			assert.Contains(t, rec.Body.String(), "apikey.error_not_manager", "body=%s", rec.Body.String())
		})
	}
	keys := x.orgKeys(t)
	require.Len(t, keys, 1)
	assert.Nil(t, keys[0].RevokedAt, "a member's revoke must not land")
	assert.Len(t, x.Store.All(), 1, "a member must mint nothing")
}

func TestProjectAPIKeys_HideTheRetiredScope(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	page := x.as(t, x.owner, http.MethodGet, "/orgs/acme/projects/alpha/apikeys", nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), `value="`+authn.ScopeAPIKeysWrite+`"`)
	assert.Contains(t, page.Body.String(), `value="`+authn.ScopeSheetsRead+`"`)
}

func TestAPIKeys_ExpiryIsRequiredAndCapped(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	for _, days := range []string{"", "0", "366", "never"} {
		rec := x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys", url.Values{"name": {"ci"}, "grant": {"all"}, "expires_in": {days}})
		assert.Contains(t, rec.Body.String(), "apikey.error_invalid_expiry", "expires_in=%q", days)
	}
	assert.Empty(t, x.Store.All(), "no key may be minted without an allowed lifetime")

	x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys", url.Values{"name": {"ci"}, "grant": {"all"}, "expires_in": {"365"}})
	keys := x.orgKeys(t)
	require.Len(t, keys, 1)
	require.NotNil(t, keys[0].ExpiresAt)
	assert.WithinDuration(t, time.Now().AddDate(0, 0, 365), *keys[0].ExpiresAt, time.Minute)
}

func (x *apikeyWebFixture) personalKeys(t *testing.T, userID uuid.UUID) []*apikey.APIKey {
	t.Helper()
	keys, err := x.Keys.ListPersonal(setTenant(context.Background(), x.org, userID))
	require.NoError(t, err)
	return keys
}

// A member — not only an owner or admin — creates their own personal access token from Settings.
func TestPersonalTokens_AMemberCreatesTheirOwn(t *testing.T) {
	x := newAPIKeyWebFixture(t)

	page := x.as(t, x.member, http.MethodGet, "/settings/tokens", nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), `name="org"`, "the form lets the caller pick an org")
	assert.Contains(t, page.Body.String(), `/settings/tokens/projects`, "switching org reloads the project picker")

	rec := x.as(t, x.member, http.MethodPost, "/settings/tokens", url.Values{
		"org": {"acme"}, "name": {"laptop"}, "scopes": {authn.ScopeSheetsRead}, "grant": {"selected"},
		"project_ids": {x.beta.ID.String()}, "expires_in": {"7"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	mine := x.personalKeys(t, x.member)
	require.Len(t, mine, 1)
	assert.Equal(t, x.member, mine[0].CreatedBy)
	assert.Equal(t, []uuid.UUID{x.beta.ID}, mine[0].ProjectIDs)
	assert.Contains(t, rec.Body.String(), "…"+mine[0].SecretHint)
	assert.Contains(t, rec.Body.String(), "/settings/tokens/acme/"+mine[0].ID.String()+"/revoke")
	assert.Empty(t, x.personalKeys(t, x.owner), "the owner's list must not show a member's token")
}

// SECURITY: a personal token is its owner's alone — the org owner cannot revoke or widen it, and the attempt changes nothing.
func TestPersonalTokens_OthersCannotTouchThem(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	x.as(t, x.member, http.MethodPost, "/settings/tokens", url.Values{
		"org": {"acme"}, "name": {"laptop"}, "grant": {"selected"}, "project_ids": {x.alpha.ID.String()}, "expires_in": {"7"},
	})
	id := x.personalKeys(t, x.member)[0].ID.String()

	x.as(t, x.owner, http.MethodPost, "/settings/tokens/acme/"+id+"/revoke", nil)
	x.as(t, x.owner, http.MethodPost, "/settings/tokens/acme/"+id+"/all-projects", nil)
	x.as(t, x.owner, http.MethodPost, "/orgs/acme/apikeys/"+id+"/revoke", nil)

	k := x.personalKeys(t, x.member)[0]
	assert.Nil(t, k.RevokedAt, "another user's revoke must not land")
	assert.False(t, k.AllProjects, "another user's promotion must not land")

	x.as(t, x.member, http.MethodPost, "/settings/tokens/acme/"+id+"/revoke", nil)
	assert.NotNil(t, x.personalKeys(t, x.member)[0].RevokedAt, "the owner revokes their own token")
}

// SECURITY: the org named by the form is membership-gated like any path slug, so a caller cannot mint into an org they do not belong to.
func TestPersonalTokens_AForeignOrgIsRefused(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	stranger := uuid.New()
	x.seedOrg(t, "other", stranger)

	rec := x.as(t, x.member, http.MethodPost, "/settings/tokens", url.Values{
		"org": {"other"}, "name": {"sneaky"}, "grant": {"all"}, "expires_in": {"7"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, x.Store.All())

	rec = x.as(t, x.member, http.MethodGet, "/settings/tokens/projects?org=other", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "the project picker must not list another org's projects")
}

func TestPersonalTokens_SignedOutIsSentToLogin(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/settings/tokens", strings.NewReader("org=acme&name=x&expires_in=7"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/login")
}

func TestPersonalTokens_UnknownOrgFallsBackToTheFirst(t *testing.T) {
	x := newAPIKeyWebFixture(t)
	page := x.as(t, x.member, http.MethodGet, "/settings/tokens?org=nope", nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), x.alpha.ID.String(), "the picker must show the selected org's projects")
}
