package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorPage_HTMXRequestGetsFragmentNotFullPage(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/projects/site/categories/x/delete", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusNotFound, "Not found", "That category no longer exists.")

	body := rec.Body.String()
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.NotContains(t, body, "<html", "an htmx error response must be a fragment, not a page")
	require.NotContains(t, body, "<!doctype", "an htmx error response must be a fragment, not a page")
	require.Contains(t, body, `class="alt-error`)
	require.Contains(t, body, "That category no longer exists.")
}

func TestErrorPage_NonHTMXRequestStillGetsFullPage(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusNotFound, "Not found", "gone")

	require.Contains(t, rec.Body.String(), "<html", "browser navigation must still get a full page")
}
