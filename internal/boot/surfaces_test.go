package boot_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/user"
)

func serveOn(t *testing.T, f *dataplaneFixture, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

func validSessionCookie(t *testing.T, f *dataplaneFixture) *http.Cookie {
	t.Helper()
	u, err := f.srv.Users.Create(t.Context(), user.CreateRequest{
		Email: "dp-cookie@example.com", Name: "DP Cookie", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	return probeCookie(t, f.srv, session.Principal{UserID: u.ID})
}

// TestDataPlaneRejectsSessionCookie enforces R2: a session cookie must not authenticate a machine surface.
func TestDataPlaneRejectsSessionCookie(t *testing.T) {
	f := newDataplaneFixture(t)
	cookie := validSessionCookie(t, f)

	console := httptest.NewRequest(http.MethodGet, "/orgs", nil)
	console.AddCookie(cookie)
	require.Less(t, serveOn(t, f, console).Code, 400,
		"the cookie must authenticate the console, or this guard proves nothing")

	withCookie := httptest.NewRequest(http.MethodGet, f.sheetURL, nil)
	withCookie.AddCookie(cookie)
	got := serveOn(t, f, withCookie)
	want := serveOn(t, f, httptest.NewRequest(http.MethodGet, f.sheetURL, nil))

	require.NotEqual(t, http.StatusOK, got.Code,
		"R2: a session cookie authenticated the data plane; body=%s", got.Body.String())
	require.Empty(t, got.Header().Values("Set-Cookie"),
		"R2: the data plane touched the session — a machine surface issues no cookies")
	require.Equal(t, want.Code, got.Code,
		"R2: the cookie changed the data plane's answer, so it is being read as a credential")
	require.Equal(t, want.Body.String(), got.Body.String(),
		"R2: the cookie changed the data plane's answer, so it is being read as a credential")
}

// TestDataPlaneRejectsJWT enforces R2 the other way: a JWT is never a data plane credential.
func TestDataPlaneRejectsJWT(t *testing.T) {
	f := newDataplaneFixture(t)
	anonymous := serveOn(t, f, httptest.NewRequest(http.MethodGet, f.sheetURL, nil))

	const jwt = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJkcC1vd25lciJ9.c2lnbmF0dXJl"
	tests := []struct{ name, header, value string }{
		{"authorization bearer", "Authorization", "Bearer " + jwt},
		{"x-api-key", "X-API-Key", jwt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, f.sheetURL, nil)
			req.Header.Set(tt.header, tt.value)
			rec := serveOn(t, f, req)

			require.NotEqual(t, http.StatusOK, rec.Code, "R4: a JWT read the data plane; body=%s", rec.Body.String())
			require.Equal(t, anonymous.Code, rec.Code,
				"R4: a JWT changed the data plane's answer for a sheet it cannot name; body=%s", rec.Body.String())
		})
	}
}

// TestSurfaceErrorShapes enforces R6: every data plane failure carries the declared JSON envelope and a code.
func TestSurfaceErrorShapes(t *testing.T) {
	f := newDataplaneFixture(t)

	tests := []struct {
		name, method, path, key string
		wantStatus              int
		wantCode                string
	}{
		{"unknown org", http.MethodGet, "/api/v1/orgs/nosuchorg/projects/dp-project/sheets/absent", "", http.StatusNotFound, apperror.CodeSheetNotFound},
		{"unknown project", http.MethodGet, "/api/v1/orgs/dp-org/projects/nosuchproject/sheets/absent", f.readKey, http.StatusNotFound, apperror.CodeSheetNotFound},
		{"unknown sheet", http.MethodGet, f.sheetURL, f.readKey, http.StatusNotFound, apperror.CodeSheetNotFound},
		{"insufficient scope", http.MethodGet, f.sheetURL, f.noneKey, http.StatusNotFound, apperror.CodeSheetNotFound},
		{"bogus credential", http.MethodGet, f.sheetURL, "key_not_a_real_key", http.StatusNotFound, apperror.CodeSheetNotFound},
		{"uncredentialed write", http.MethodDelete, f.sheetURL + "/cache", "", http.StatusNotFound, apperror.CodeSheetNotFound},
		{"subtree root", http.MethodGet, "/api/v1/", "", http.StatusNotFound, apperror.CodeSheetNotFound},
		{"typo'd path", http.MethodGet, f.sheetURL + "/nosuchverb", f.readKey, http.StatusNotFound, apperror.CodeSheetNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.key != "" {
				req.Header.Set("Authorization", "Bearer "+tt.key)
			}
			rec := serveOn(t, f, req)
			body := rec.Body.String()

			require.Equal(t, tt.wantStatus, rec.Code, "body=%s", body)
			require.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json"),
				"R6: Content-Type = %q, want application/json — the data plane's error shape is fixed",
				rec.Header().Get("Content-Type"))
			require.NotContains(t, body, "<html",
				"R6: an HTML error page reached a machine surface")

			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
				"R6: the error body is not the declared envelope: %s", body)
			require.Equal(t, tt.wantCode, envelope.Error.Code,
				"R6: the error envelope must carry the declared code; body=%s", body)
			require.NotEmpty(t, envelope.Error.Message, "R6: the error envelope carries no message; body=%s", body)
		})
	}

	console := serveOn(t, f, httptest.NewRequest(http.MethodGet, "/nosuchconsolepage", nil))
	require.NotContains(t, console.Header().Get("Content-Type"), "application/json",
		"R6 is per surface: the console must not share the data plane's error shape")
}
