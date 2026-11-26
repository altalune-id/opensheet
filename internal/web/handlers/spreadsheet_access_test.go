package handlers_test

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/handlers"
	"altalune.id/opensheet/internal/web/middleware"
)

func (f *sheetsFixture) doHTMX(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := f.authedRequest(t, method, target, body, f.Principal)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	f.mux(t).ServeHTTP(rec, req)
	return rec
}

func (f *sheetsFixture) doEN(t *testing.T, method, target, body string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := f.authedRequest(t, method, target, body, f.Principal)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	tr := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	req = req.WithContext(i18n.TranslatorInto(req.Context(), tr))
	rec := httptest.NewRecorder()
	f.mux(t).ServeHTTP(rec, req)
	return rec
}

func (f *sheetsFixture) pickRoute(credID uuid.UUID) string {
	return f.path("/spreadsheets/pick?credential_id=" + credID.String())
}

func (f *sheetsFixture) seedForeignCredential(t *testing.T) *credential.Credential {
	t.Helper()
	proj, err := f.Projects.Create(setTenant(context.Background(), f.Org.ID, f.Principal.UserID), f.Org.ID, "other", "Other")
	require.NoError(t, err)
	id := uuid.Must(uuid.NewV7())
	box, err := f.Sealer.Seal([]byte("1//refresh-token"), credential.SealAAD(f.Org.ID, proj.ID, id))
	require.NoError(t, err)
	c, err := credential.New(id, f.Org.ID, proj.ID, f.Principal.UserID, "Elsewhere", credential.KindGoogleOAuth, "other@example.com", box)
	require.NoError(t, err)
	f.CredStore.Seed(c)
	return c
}

func registerForm(credID uuid.UUID) string {
	return url.Values{"credential_id": {credID.String()}, "google_file_id": {testFileID}, "title": {"Q1 Report"}}.Encode()
}

func text(rec *httptest.ResponseRecorder) string { return html.UnescapeString(rec.Body.String()) }

func TestSpreadsheetHandler_PickUsesTheSelectedCredential(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	first := f.seedCredential(t, credential.KindGoogleOAuth, "First")
	second := f.seedCredential(t, credential.KindGoogleOAuth, "Second")

	rec := f.do(t, http.MethodGet, f.pickRoute(second.ID), "")
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	key := f.Deps.Key(web.PurposeGooglePicker)
	assert.Equal(t, f.pickerPath(t, second.ID), rec.Header().Get("Location"))
	assert.NotContains(t, rec.Header().Get("Location"),
		url.QueryEscape(handlers.PickerStateFor(key, f.Org.Slug, f.Project.Slug, first.ID)))
}

func TestSpreadsheetHandler_PickRefusesReauthAndServiceAccount(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	sa := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	stale := f.seedCredential(t, credential.KindGoogleOAuth, "Stale")
	_, err := f.Creds.MarkReauthNeeded(f.ctx(), stale.ID)
	require.NoError(t, err)

	rec := f.do(t, http.MethodGet, f.pickRoute(sa.ID), "")
	assert.Equal(t, http.StatusConflict, rec.Code, "a service account token must never reach the browser")

	rec = f.do(t, http.MethodGet, f.pickRoute(stale.ID), "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "reconnecting")
}

func TestSpreadsheetHandler_PickIsProjectScoped(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	foreign := f.seedForeignCredential(t)

	rec := f.do(t, http.MethodGet, f.pickRoute(foreign.ID), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"))

	rec = f.do(t, http.MethodGet, f.path("/spreadsheets/pick?credential_id=not-a-uuid"), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSpreadsheetHandler_PickIsGatedOnTheCapability(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t, func(c *capabilities.Capabilities) { c.GooglePicker = false })
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.do(t, http.MethodGet, f.pickRoute(c.ID), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGoogleConnectHandler_PickerRendersEveryState(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.doEN(t, http.MethodGet, f.pickerPath(t, c.ID), "", false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, state := range []string{"loading", "registering", "cancelled", "stuck"} {
		assert.Regexp(t, `data-picker-state="`+state+`"`, body)
	}
	assert.Contains(t, body, "<details", "the help disclosure must always render")
	assert.Contains(t, body, "data-picker-help")
	assert.Contains(t, text(rec), "Picking as sa@example.com")

	form := regexp.MustCompile(`(?s)<form[^>]*data-picker-register[^>]*>.*?</form>`).FindString(body)
	require.NotEmpty(t, form, "the auto-register form must render")
	assert.Contains(t, form, `method="post"`)
	assert.Contains(t, form, `action="`+f.path("/spreadsheets")+`"`)
	assert.NotContains(t, form, "hx-", "the auto-register form is a plain post")
	assert.Regexp(t, `name="credential_id"[^>]*value="`+c.ID.String()+`"|value="`+c.ID.String()+`"[^>]*name="credential_id"`, form)
	assert.Contains(t, form, `name="google_file_id"`)
	assert.Contains(t, form, `name="title"`)

	assert.Contains(t, body, "pickerLoadTimeoutMs = 15000")
	assert.Contains(t, body, "createElement('script')")
	assert.Contains(t, body, "addEventListener('error'")
	assert.NotRegexp(t, `<[a-z]+[^>]*\son[a-z]+=`, body, "CSP blocks inline handler attributes")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), "the page holds a live Google access token")

	csp := httptest.NewRecorder()
	middleware.CSP(middleware.CSPOptions{Enabled: true})(f.mux(t)).
		ServeHTTP(csp, f.authedRequest(t, http.MethodGet, f.pickerPath(t, c.ID), "", f.Principal))
	require.Equal(t, http.StatusOK, csp.Code)
	nonce := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(csp.Header().Get("Content-Security-Policy"))
	require.Len(t, nonce, 2)
	scripts := regexp.MustCompile(`(?s)(<script[^>]*>).*?</script>`).FindAllStringSubmatch(csp.Body.String(), -1)
	require.NotEmpty(t, scripts)
	for _, m := range scripts {
		assert.Contains(t, m[1], `nonce="`+nonce[1]+`"`, m[1])
	}
}

func TestSpreadsheetHandler_CreatePlainPostRedirectsToDetailWithNotice(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets"), registerForm(c.ID))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	items, err := f.Sprds.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	want := f.path("/spreadsheets/" + items[0].ID.String() + "?added=1")
	require.Equal(t, want, rec.Header().Get("Location"))

	page := f.doEN(t, http.MethodGet, want, "", false)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, text(page), "Spreadsheet added. Next, publish a tab.")
}

func TestSpreadsheetHandler_CreateHTMXStillRendersTheFragment(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.doHTMX(t, http.MethodPost, f.path("/spreadsheets"), registerForm(c.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `id="spreadsheets-section"`)
	assert.Contains(t, rec.Body.String(), "Q1 Report")
}

func TestSpreadsheetHandler_CreatePlainPostForARegisteredFileRedirectsRestored(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets"), registerForm(c.ID))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	want := f.path("/spreadsheets/" + sp.ID.String() + "?restored=1")
	require.Equal(t, want, rec.Header().Get("Location"))

	page := f.doEN(t, http.MethodGet, want, "", false)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, text(page), "Access restored. OpenSheet can open this file again.")
}

func TestSpreadsheetHandler_CreatePlainPostWithAnotherCredentialLandsWithoutRestored(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	bound := f.seedCredential(t, credential.KindGoogleOAuth, "Bound")
	other := f.seedCredential(t, credential.KindGoogleOAuth, "Other")
	sp := f.seedSpreadsheet(t, bound.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets"), registerForm(other.ID))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	want := f.path("/spreadsheets/" + sp.ID.String())
	require.Equal(t, want, rec.Header().Get("Location"),
		"a pick with another account restores nothing for the bound credential")

	page := f.doEN(t, http.MethodGet, want, "", false)
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), `data-notice="spreadsheet-restored"`)
}

func TestSpreadsheetHandler_CreatePlainPostFailureRendersTheFullPage(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets"),
		url.Values{"credential_id": {c.ID.String()}, "google_file_id": {"not a file id!"}}.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<html")
	assert.Contains(t, rec.Body.String(), `data-error="spreadsheets"`)
}

func TestSpreadsheetHandler_RegisterUnreachableFileShowsPickCallout(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	f.Google.hideDocument()

	rec := f.doEN(t, http.MethodPost, f.path("/spreadsheets"), registerForm(c.ID), true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `data-no-access="google_oauth"`)
	assert.Contains(t, text(rec), "OpenSheet can't open this file")
	assert.Contains(t, text(rec), `href="`+f.pickRoute(c.ID)+`"`)
	assert.Contains(t, rec.Body.String(), apperror.CodeGoogleNotFound)

	items, err := f.Sprds.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items, "an unreachable file must not be registered")
}

func TestSpreadsheetHandler_RegisterUnsharedFileShowsServiceAccountEmail(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	f.Google.hideDocument()

	rec := f.doEN(t, http.MethodPost, f.path("/spreadsheets"), registerForm(c.ID), true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `data-no-access="service_account"`)
	assert.Contains(t, text(rec), "Share the spreadsheet with sa@example.com, then try again.")
	assert.NotContains(t, rec.Body.String(), "/spreadsheets/pick")
}

func TestCredentialHandler_ConnectedNoticeNamesTheAccount(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.doEN(t, http.MethodGet, f.path("/credentials?connected="+c.ID.String()), "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-notice="credential-connected"`)
	assert.Contains(t, text(rec), "Google account sa@example.com connected.")
	assert.Contains(t, text(rec), "Next: add a spreadsheet")
	assert.Contains(t, rec.Body.String(), `href="`+f.path("/spreadsheets")+`"`)
}

func TestCredentialHandler_ConnectedNoticeIgnoresForeignID(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	foreign := f.seedForeignCredential(t)

	for _, id := range []string{foreign.ID.String(), uuid.NewString(), "not-a-uuid"} {
		rec := f.do(t, http.MethodGet, f.path("/credentials?connected="+id), "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), `data-notice="credential-connected"`)
		assert.NotContains(t, rec.Body.String(), "other@example.com")
	}
}

func TestSpreadsheetHandler_TabsQuotaFailureKeepsThePlainMessage(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)
	f.Google.breakGoogle()

	for _, target := range []string{"/tabs", "/publish"} {
		rec := f.doEN(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+target), "", false)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), apperror.CodeGoogleUnavailable)
		assert.NotContains(t, rec.Body.String(), "data-no-access", target)
		assert.True(t, strings.Contains(text(rec), "Could not read the document's tabs from Google."), target)
	}
}

func (f *sheetsFixture) seedOthersCredential(t *testing.T, name string) *credential.Credential {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	box, err := f.Sealer.Seal([]byte("1//refresh-token"), credential.SealAAD(f.Org.ID, f.Project.ID, id))
	require.NoError(t, err)
	c, err := credential.New(id, f.Org.ID, f.Project.ID, uuid.New(), name, credential.KindGoogleOAuth, "teammate@example.com", box)
	require.NoError(t, err)
	f.CredStore.Seed(c)
	return c
}

const notConnectorCopy = "Only the person who connected this Google account can pick files with it."

func TestSpreadsheetHandler_PickRefusesAnotherMembersCredential(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedOthersCredential(t, "Teammate")

	rec := f.doEN(t, http.MethodGet, f.pickRoute(c.ID), "", false)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"), "no Picker state may be signed for another member's account")
	assert.Contains(t, text(rec), notConnectorCopy)
}

func TestGoogleConnectHandler_PickerRefusesAnotherMembersCredential(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedOthersCredential(t, "Teammate")

	rec := f.doEN(t, http.MethodGet, f.pickerPath(t, c.ID), "", false)
	assert.Equal(t, http.StatusForbidden, rec.Code, "a shared Picker link must not open for another member")
	assert.NotContains(t, rec.Body.String(), testAccessToken)
	assert.Zero(t, f.Google.tokenCalls(), "no access token may be minted for another member's account")
	assert.Contains(t, text(rec), notConnectorCopy)
}

func TestSpreadsheetHandler_PickFormListsOnlyOwnCredentials(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	others := f.seedOthersCredential(t, "Teammate")

	rec := f.do(t, http.MethodGet, f.path("/spreadsheets"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "data-pick-form", "with no own Google account the pick form is hidden")

	own := f.seedCredential(t, credential.KindGoogleOAuth, "Mine")
	rec = f.do(t, http.MethodGet, f.path("/spreadsheets"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	form := regexp.MustCompile(`(?s)<form[^>]*data-pick-form[^>]*>.*?</form>`).FindString(rec.Body.String())
	require.NotEmpty(t, form)
	assert.Contains(t, form, own.ID.String())
	assert.NotContains(t, form, others.ID.String())
}

func TestSpreadsheetHandler_NoAccessCalloutOffersPickOnlyToTheConnector(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedOthersCredential(t, "Teammate")
	sp := f.seedSpreadsheet(t, c.ID)
	f.Google.hideDocument()

	ask := "Ask teammate@example.com's owner to pick it again, or connect your own Google account."
	register := f.doEN(t, http.MethodPost, f.path("/spreadsheets"),
		url.Values{"credential_id": {c.ID.String()}, "google_file_id": {"1OtherFileId"}}.Encode(), true)
	tabs := f.doEN(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+"/tabs"), "", false)
	bulk := f.doEN(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), "", false)
	for name, rec := range map[string]*httptest.ResponseRecorder{"register": register, "tabs": tabs, "bulk": bulk} {
		require.Equal(t, http.StatusOK, rec.Code, name)
		assert.Contains(t, rec.Body.String(), `data-no-access="google_oauth"`, name)
		assert.NotContains(t, rec.Body.String(), "/spreadsheets/pick", name)
		assert.Contains(t, text(rec), ask, name)
	}
}
