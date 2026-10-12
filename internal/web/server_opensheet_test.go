package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/web"
)

// TestServer_DataPlaneWinsTheAPIV1Subtree locks the mount precedence the data plane depends on: Go 1.22+ ServeMux resolves /api/v1/ over /api/ by specificity, so registration order is irrelevant.
func TestServer_DataPlaneWinsTheAPIV1Subtree(t *testing.T) {
	t.Parallel()
	echo := func(label string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(label + " " + r.URL.Path))
		})
	}
	handler := web.NewServer(web.ServerOpts{
		BasePath:    "/app",
		APIHandler:  echo("api"),
		DataHandler: echo("data"),
	})

	cases := map[string]string{
		"/app/api/v1/orgs/acme/projects/default/sheets/prices": "data /app/api/v1/orgs/acme/projects/default/sheets/prices",
		"/app/api/v1/":                      "data /app/api/v1/",
		"/app/api/todo.v1.TodoService/List": "api /app/api/todo.v1.TodoService/List",
		"/app/api/openapi.yaml":             "api /app/api/openapi.yaml",
	}
	for path, want := range cases {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Equal(t, want, rec.Body.String(), "%s landed on the wrong handler", path)
	}
}

// TestServer_NilDataHandlerDoesNotPanic covers healthOnlyHandler and every ServerOpts built without a data plane.
func TestServer_NilDataHandlerDoesNotPanic(t *testing.T) {
	t.Parallel()
	var handler http.Handler
	require.NotPanics(t, func() {
		handler = web.NewServer(web.ServerOpts{BasePath: "/app"})
	})

	req := httptest.NewRequest(http.MethodGet, "/app/api/v1/orgs/acme/projects/default/sheets/prices", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServer_DataPlaneMountsWithoutTheRPCSurface locks that api.enabled=false (a nil APIHandler) still serves rows.
func TestServer_DataPlaneMountsWithoutTheRPCSurface(t *testing.T) {
	t.Parallel()
	handler := web.NewServer(web.ServerOpts{
		BasePath: "/app",
		DataHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("data"))
		}),
	})

	req := httptest.NewRequest(http.MethodGet, "/app/api/v1/orgs/acme/projects/default/sheets/prices", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "data", rec.Body.String())
}
