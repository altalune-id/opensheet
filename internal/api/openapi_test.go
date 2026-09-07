package api_test

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/template/internal/api"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/todo"
)

func openAPIServer(t *testing.T, enabled bool, auth *api.BasicAuth) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)
	orgs := fakes.NewOrg()
	projs := fakes.NewProject()
	tds := fakes.NewTodo()

	kernel := &platform.Kernel{
		Log:      log,
		Reporter: reporter,
		Verifier: stubVerifier{principal: session.Principal{}},
	}
	srv := api.New(
		nil, kernel,
		nil, nil,
		org.NewService(orgs, capabilities.Capabilities{OrgCreation: true}, log, reporter.Unexpected),
		project.NewService(projs, log, reporter.Unexpected),
		todo.NewService(tds, log, reporter.Unexpected),
		nil,
		tds,
	)
	srv.OpenAPIEnabled = enabled
	srv.OpenAPIBasicAuth = auth

	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)
	return ts
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestOpenAPI_Disabled_Returns404(t *testing.T) {
	ts := openAPIServer(t, false, nil)

	for _, path := range []string{"/api/openapi.yaml", "/api/openapi.json", "/api/docs"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("path=%s got status=%d, want 404", path, resp.StatusCode)
		}
	}
}

func TestOpenAPI_Enabled_NoAuth_ReturnsYAML(t *testing.T) {
	ts := openAPIServer(t, true, nil)

	resp, err := http.Get(ts.URL + "/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("content-type=%q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	if !strings.Contains(string(body), "openapi:") {
		t.Errorf("body missing openapi header")
	}
}

func TestOpenAPI_Enabled_NoAuth_ReturnsJSON(t *testing.T) {
	ts := openAPIServer(t, true, nil)

	resp, err := http.Get(ts.URL + "/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type=%q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "\"openapi\"") {
		t.Errorf("body missing openapi key")
	}
}

func TestOpenAPI_BasicAuth_MissingHeader_Returns401(t *testing.T) {
	ts := openAPIServer(t, true, &api.BasicAuth{User: "docs", Password: "s3cret"})

	resp, err := http.Get(ts.URL + "/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `Basic realm="altempl openapi"`) {
		t.Errorf("WWW-Authenticate=%q", got)
	}
}

func TestOpenAPI_BasicAuth_ValidCreds_Returns200(t *testing.T) {
	ts := openAPIServer(t, true, &api.BasicAuth{User: "docs", Password: "s3cret"})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/openapi.yaml", nil)
	req.Header.Set("Authorization", basicHeader("docs", "s3cret"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

func TestOpenAPI_BasicAuth_WrongPassword_Returns401(t *testing.T) {
	ts := openAPIServer(t, true, &api.BasicAuth{User: "docs", Password: "s3cret"})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/openapi.yaml", nil)
	req.Header.Set("Authorization", basicHeader("docs", "nope"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestOpenAPI_BasicAuth_WrongUser_Returns401(t *testing.T) {
	ts := openAPIServer(t, true, &api.BasicAuth{User: "docs", Password: "s3cret"})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/openapi.yaml", nil)
	req.Header.Set("Authorization", basicHeader("wrong", "s3cret"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestOpenAPI_Docs_Enabled_NoAuth_ReturnsHTML(t *testing.T) {
	ts := openAPIServer(t, true, nil)

	resp, err := http.Get(ts.URL + "/api/docs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type=%q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "swagger-ui") {
		t.Errorf("body missing swagger-ui marker")
	}
	if !strings.Contains(string(body), "/api/openapi.yaml") {
		t.Errorf("body missing spec URL")
	}
}

func TestOpenAPI_Docs_BasicAuth_MissingHeader_Returns401(t *testing.T) {
	ts := openAPIServer(t, true, &api.BasicAuth{User: "docs", Password: "s3cret"})

	resp, err := http.Get(ts.URL + "/api/docs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `Basic realm="altempl openapi"`) {
		t.Errorf("WWW-Authenticate=%q — must match spec-endpoint realm so browsers reuse creds", got)
	}
}

func TestOpenAPI_Docs_BasicAuth_ValidCreds_Returns200(t *testing.T) {
	ts := openAPIServer(t, true, &api.BasicAuth{User: "docs", Password: "s3cret"})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/docs", nil)
	req.Header.Set("Authorization", basicHeader("docs", "s3cret"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}
