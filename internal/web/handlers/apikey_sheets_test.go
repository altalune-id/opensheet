package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web/handlers"
	"altalune.id/opensheet/internal/web/middleware"
)

type pinnedKeysFixture struct {
	*sheetsFixture
	Keys   *apikey.Service
	Store  *fakes.APIKey
	rates  *sheet.Sheet
	salary *sheet.Sheet
}

func newPinnedKeysFixture(t *testing.T) *pinnedKeysFixture {
	t.Helper()
	f := newSheetsFixture(t)
	sp := f.seedSpreadsheet(t, f.seedCredential(t, credential.KindGoogleOAuth, "Connected").ID)
	store := fakes.NewAPIKey()
	keys := apikey.NewService(store, apikey.Scheme{}, f.Orgs, projectCatalog{svc: f.Projects}, discardLogger(), passthroughUnexpected())
	return &pinnedKeysFixture{
		sheetsFixture: f, Keys: keys, Store: store,
		rates:  f.seedSheet(t, sp.ID, "rates", sheet.VisibilityKey),
		salary: f.seedSheet(t, sp.ID, "salary", sheet.VisibilityKey),
	}
}

func (x *pinnedKeysFixture) serve(t *testing.T, h *handlers.APIKeyHandler, method string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	r := x.authedRequest(t, method, x.path("/apikeys"), form.Encode(), x.Principal)
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func (x *pinnedKeysFixture) pinned() *handlers.APIKeyHandler {
	return handlers.NewAPIKeyHandler(x.Deps, x.Projects, x.Keys).WithResources(handlers.SheetResources(x.Sheets))
}

func (x *pinnedKeysFixture) mintForm(resourceIDs ...string) url.Values {
	return url.Values{"name": {"ci"}, "scopes": {authn.ScopeSheetsRead}, "expires_in": {"30"}, "resource_ids": resourceIDs}
}

func (x *pinnedKeysFixture) keys(t *testing.T) []*apikey.APIKey {
	t.Helper()
	keys, err := x.Keys.List(x.ctx(), x.Project.ID)
	require.NoError(t, err)
	return keys
}

func TestPinnedAPIKeys_FormOffersTheProjectsSheets(t *testing.T) {
	t.Parallel()
	x := newPinnedKeysFixture(t)

	rec := x.serve(t, x.pinned(), http.MethodGet, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `name="resource_ids"`)
	assert.Contains(t, body, `value="`+x.rates.ID.String()+`"`)
	assert.Contains(t, body, `value="`+x.salary.ID.String()+`"`)
	assert.Contains(t, body, "apikey.resources_label")
}

func TestPinnedAPIKeys_SelectedSheetsReachMint(t *testing.T) {
	t.Parallel()
	x := newPinnedKeysFixture(t)

	rec := x.serve(t, x.pinned(), http.MethodPost, x.mintForm(x.rates.ID.String(), x.rates.ID.String()))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	keys := x.keys(t)
	require.Len(t, keys, 1)
	assert.Equal(t, []uuid.UUID{x.rates.ID}, keys[0].ResourceIDs)
	assert.Contains(t, rec.Body.String(), ">rates<", "the list names the granted sheet")
}

func TestPinnedAPIKeys_NoSelectionIsProjectWide(t *testing.T) {
	t.Parallel()
	x := newPinnedKeysFixture(t)

	rec := x.serve(t, x.pinned(), http.MethodPost, x.mintForm())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	keys := x.keys(t)
	require.Len(t, keys, 1)
	assert.Empty(t, keys[0].ResourceIDs)
	assert.Contains(t, rec.Body.String(), "apikey.all_resources")
}

// SECURITY: a sheet id the project does not own is refused, never handed to Mint.
func TestPinnedAPIKeys_ForeignSheetIsRefused(t *testing.T) {
	t.Parallel()
	x := newPinnedKeysFixture(t)

	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		rec := x.serve(t, x.pinned(), http.MethodPost, x.mintForm(x.rates.ID.String(), id))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "apikey.error_resource", "id=%q", id)
	}
	assert.Empty(t, x.Store.All(), "no key may be minted with a foreign sheet")
}

func TestPinnedAPIKeys_UnsetHookKeepsTemplateBehavior(t *testing.T) {
	t.Parallel()
	x := newPinnedKeysFixture(t)
	plain := handlers.NewAPIKeyHandler(x.Deps, x.Projects, x.Keys)

	page := x.serve(t, plain, http.MethodGet, nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), `name="resource_ids"`)

	rec := x.serve(t, plain, http.MethodPost, x.mintForm(x.rates.ID.String()))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	keys := x.keys(t)
	require.Len(t, keys, 1)
	assert.Empty(t, keys[0].ResourceIDs, "without the hook a key stays project-wide")
	assert.NotContains(t, rec.Body.String(), "apikey.resources_label")
}

func TestGooglePicker_CSPAdmitsGoogleOnThePickerOnly(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	h := middleware.CSP(middleware.CSPOptions{Enabled: true})(f.mux(t))

	get := func(target string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, target, "", f.Principal))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Header().Get("Content-Security-Policy")
	}

	picker := get(f.pickerPath(t, c.ID))
	assert.Contains(t, picker, "script-src 'self' 'nonce-")
	assert.Contains(t, picker, "https://apis.google.com")
	assert.Contains(t, picker, "frame-src 'self' https://docs.google.com")
	assert.Contains(t, picker, "frame-ancestors 'none'", "the rest of the policy is kept")

	for _, target := range []string{f.path("/spreadsheets"), f.path("/credentials")} {
		other := get(target)
		assert.NotContains(t, other, "google.com", "target=%s", target)
		assert.NotContains(t, other, "frame-src", "target=%s", target)
	}
}
