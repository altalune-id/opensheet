package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/web"
)

// stubRegister is a minimal Register that answers a fixed route.
type stubRegister struct {
	path, body string
}

func (s stubRegister) Register(mux *http.ServeMux) {
	mux.HandleFunc(s.path, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(s.body))
	})
}

func TestServer_HealthAndRobots(t *testing.T) {
	t.Parallel()
	handler := web.NewServer(web.ServerOpts{BasePath: "/app"})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	for _, path := range []string{"/healthz", "/robots.txt"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status=%d", path, resp.StatusCode)
		}
	}
}

func TestServer_Readyz_Unready(t *testing.T) {
	t.Parallel()
	handler := web.NewServer(web.ServerOpts{
		BasePath: "/app",
		HealthOK: func() bool { return false },
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d", resp.StatusCode)
	}
}

func TestServer_MountsAppUnderBasePath(t *testing.T) {
	t.Parallel()
	handler := web.NewServer(web.ServerOpts{
		BasePath:    "/app",
		AppHandlers: []web.Register{stubRegister{path: "GET /login", body: "login-page"}},
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/app/login")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "login-page" {
		t.Errorf("body=%q", string(body))
	}
}

func TestServer_BareBasePathRedirects(t *testing.T) {
	t.Parallel()
	handler := web.NewServer(web.ServerOpts{BasePath: "/app"})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(ts.URL + "/app")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("status=%d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/app/" {
		t.Errorf("Location=%q", loc)
	}
}

func TestServer_MiddlewareChain_OuterFirst(t *testing.T) {
	t.Parallel()
	var order []string
	mw := func(label string) web.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "before:"+label)
				next.ServeHTTP(w, r)
				order = append(order, "after:"+label)
			})
		}
	}
	handler := web.NewServer(web.ServerOpts{
		BasePath:    "",
		AppHandlers: []web.Register{stubRegister{path: "GET /x", body: "ok"}},
		Middlewares: []web.Middleware{mw("outer"), mw("inner")},
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	want := "before:outer,before:inner,after:inner,after:outer"
	got := strings.Join(order, ",")
	if got != want {
		t.Errorf("order=%q, want %q", got, want)
	}
}

func TestReadyz_UnreadyBeforeFirstProbe(t *testing.T) {
	t.Parallel()
	var ready atomic.Bool
	ts := httptest.NewServer(web.NewServer(web.ServerOpts{HealthOK: ready.Load}))
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/readyz")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	ready.Store(true)
	resp, err = http.Get(ts.URL + "/readyz")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHealthz_IgnoresDBHealth(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(web.NewServer(web.ServerOpts{HealthOK: func() bool { return false }}))
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode, "liveness must not depend on the database")
}
