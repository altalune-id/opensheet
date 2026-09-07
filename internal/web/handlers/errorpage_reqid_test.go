package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/reqid"
)

func TestErrorPage_ShowsRequestIDForCorrelation(t *testing.T) {
	f := newFixture(t)
	const id = "01a07918-5597-7eff-8ac8-375710ffbf82"

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	req = req.WithContext(reqid.WithContext(req.Context(), id))
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusInternalServerError, "Members failed", "Could not load members.")

	body := rec.Body.String()
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, body, "Members failed")
	require.Contains(t, body, id, "the error page must show the request id, or a user cannot quote it to find the log line")
}

func TestErrorPage_OmitsReferenceWhenNoRequestID(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusNotFound, "Org not found", "")

	body := rec.Body.String()
	require.Contains(t, body, "Org not found")
	require.NotContains(t, body, "select-all", "an empty reference block must not render")
}

func TestErrorPage_RequestIDMatchesTheLoggedOne(t *testing.T) {
	f := newFixture(t)

	// The middleware mints the id and the logger reads it from the same context, so the page
	// must read it from there too or screen and log will disagree.
	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	ctx, id := reqid.Ensure(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusInternalServerError, "Members failed", "")

	require.Equal(t, id, reqid.FromContext(req.Context()))
	require.True(t, strings.Contains(rec.Body.String(), id), "page id must equal the context id the logger uses")
}
