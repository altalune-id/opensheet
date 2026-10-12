package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/web/handlers"
)

func (f *sheetsFixture) overviewMux() *http.ServeMux {
	mux := http.NewServeMux()
	handlers.NewOpensheetOverviewHandler(f.Deps, f.Projects, f.Creds, f.Sprds, f.Sheets).Register(mux)
	return mux
}

func (f *sheetsFixture) getOverview(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.overviewMux().ServeHTTP(rec, f.authedRequest(t, http.MethodGet, target, "", f.Principal))
	return rec
}

func countOf(body, key string) string {
	m := regexp.MustCompile(`data-count="` + key + `"[^>]*>([0-9]+)<`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestOpensheetOverview_RendersCountsAndLinks(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "reader")
	f.seedCredential(t, credential.KindServiceAccount, "writer")
	sp := f.seedSpreadsheet(t, c.ID)
	f.seedSheet(t, sp.ID, "people", sheet.VisibilityKey)

	rec := f.getOverview(t, f.path("/overview"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()

	assert.Equal(t, "2", countOf(body, "credentials"))
	assert.Equal(t, "1", countOf(body, "spreadsheets"))
	assert.Equal(t, "1", countOf(body, "sheets"))
	for _, suffix := range []string{"/credentials", "/spreadsheets", "/sheets", "/apikeys"} {
		assert.Contains(t, body, `href="`+f.path(suffix)+`"`, "overview must link to %s", suffix)
	}
	assert.Contains(t, body, `data-ready="overview"`)
	assert.NotContains(t, body, `data-empty="overview"`, "a fully set-up project shows no setup checklist")
	assert.Contains(t, body, f.Project.ID.String(), "overview shows the project id")
}

func TestOpensheetOverview_EmptyProjectShowsTheSetupOrder(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)

	rec := f.getOverview(t, f.path("/overview"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()

	require.Contains(t, body, `data-empty="overview"`)
	for _, key := range []string{"credentials", "spreadsheets", "sheets"} {
		assert.Equal(t, "0", countOf(body, key), key)
	}
	steps := regexp.MustCompile(`data-step="([a-z]+)"`).FindAllStringSubmatch(body, -1)
	order := make([]string, 0, len(steps))
	for _, s := range steps {
		order = append(order, s[1])
	}
	assert.Equal(t, []string{"credential", "spreadsheet", "sheet", "apikey"}, order, "setup steps must be in setup order")
	assert.Regexp(t, `data-step="credential"[^>]*data-step-next`, body, "connecting a credential is the first call to action")
	assert.Contains(t, body, `href="`+f.path("/credentials")+`" data-step-action="credential"`)
}

func TestOpensheetOverview_NextStepFollowsProgress(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	f.seedCredential(t, credential.KindServiceAccount, "reader")

	body := f.getOverview(t, f.path("/overview")).Body.String()
	assert.Regexp(t, `data-step="credential"[^>]*data-step-done`, body)
	assert.Regexp(t, `data-step="spreadsheet"[^>]*data-step-next`, body)
}

func TestOpensheetOverview_ForeignOrgOrProjectIs404(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	other := f.seedOrg(t, "rival", uuid.New())
	_, err := f.Projects.Create(setTenant(context.Background(), other.ID, other.OwnerID), other.ID, "default", "Default")
	require.NoError(t, err)

	for _, target := range []string{
		"/orgs/acme/projects/ghost/overview",
		"/orgs/rival/projects/default/overview",
	} {
		t.Run(target, func(t *testing.T) {
			assert.Equal(t, http.StatusNotFound, f.getOverview(t, target).Code)
		})
	}
}

func TestOpensheetOverview_UnauthRedirectsToLogin(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := httptest.NewRecorder()
	f.overviewMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, f.path("/overview"), nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}
