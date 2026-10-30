package boot_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/reqid"
)

type logLine struct {
	msg   string
	path  string
	reqID string
}

type captureLog struct {
	mu    sync.Mutex
	lines []logLine
}

func (c *captureLog) Enabled(context.Context, slog.Level) bool { return true }

func (c *captureLog) Handle(ctx context.Context, r slog.Record) error {
	line := logLine{msg: r.Message, reqID: reqid.FromContext(ctx)}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "path" {
			line.path = a.Value.String()
		}
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
	return nil
}

func (c *captureLog) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *captureLog) WithGroup(string) slog.Handler { return c }

func (c *captureLog) warned(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range c.lines {
		if strings.Contains(line.msg, substr) {
			return true
		}
	}
	return false
}

func (c *captureLog) logged(path, id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.lines, func(l logLine) bool {
		return l.msg == "http.request" && l.path == path && l.reqID == id
	})
}

type edgeFixture struct {
	srv *boot.Server
	log *captureLog
}

func newEdgeFixture(t *testing.T) *edgeFixture {
	t.Helper()

	cfg := newSmokeCfg(t)
	cfg.API.Enabled = true
	cfg.HTTP.CSP.Enabled = true

	capture := &captureLog{}
	srv, err := boot.BootServer(context.Background(), cfg, boot.WithLogger(slog.New(capture)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	require.NotNil(t, srv.Web, "the web handler must be wired")

	return &edgeFixture{srv: srv, log: capture}
}

func (f *edgeFixture) get(t *testing.T, path, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if id != "" {
		req.Header.Set(reqid.Header, id)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

func assertEdgeChainRan(t *testing.T, f *edgeFixture, surface, path string) {
	t.Helper()

	t.Run(surface+" echoes an inbound request id", func(t *testing.T) {
		const id = "edge-chain-probe-0123456789"
		rec := f.get(t, path, id)
		require.Equal(t, id, rec.Header().Get(reqid.Header),
			"%s lost webmw.RequestID: %s answered without echoing %s", surface, path, reqid.Header)
		require.True(t, f.log.logged(path, id),
			"%s lost webmw.RequestLog: no http.request line for %s carrying request id %q", surface, path, id)
	})

	t.Run(surface+" mints a request id when none is sent", func(t *testing.T) {
		rec := f.get(t, path, "")
		minted := rec.Header().Get(reqid.Header)
		require.NotEmpty(t, minted,
			"%s lost webmw.RequestID: %s answered with no %s at all", surface, path, reqid.Header)
		require.True(t, f.log.logged(path, minted),
			"%s lost webmw.RequestLog: no http.request line for %s carrying minted id %q", surface, path, minted)
	})
}

// TestControlPlaneRunsTheEdgeChain guards boot's Control chain behaviourally.
func TestControlPlaneRunsTheEdgeChain(t *testing.T) {
	f := newEdgeFixture(t)
	const path = "/api/project.v1.ProjectService/ListProjects"

	require.NotEmpty(t, f.get(t, "/login", "").Header().Get("Content-Security-Policy"),
		"the console chain must set CSP, or the routing check below proves nothing")
	require.Empty(t, f.get(t, path, "").Header().Get("Content-Security-Policy"),
		"the control plane answered from the console chain, so this guard would not be testing Control")

	assertEdgeChainRan(t, f, "the control plane", path)
}

// TestProbesRunTheEdgeChain guards boot's Probes chain behaviourally.
func TestProbesRunTheEdgeChain(t *testing.T) {
	f := newEdgeFixture(t)

	for _, path := range []string{"/healthz", "/readyz", "/robots.txt"} {
		require.Empty(t, f.get(t, path, "").Header().Get("Content-Security-Policy"),
			"%s answered from the console chain, so this guard would not be testing Probes", path)
		assertEdgeChainRan(t, f, "probe "+path, path)
	}
}

// TestIngestRunsTheEdgeChain guards boot's Ingest chain behaviourally.
func TestIngestRunsTheEdgeChain(t *testing.T) {
	f := newEdgeFixture(t)
	const path = "/hooks/noprovider/events"

	require.Empty(t, f.get(t, path, "").Header().Get("Content-Security-Policy"),
		"ingest answered from the console chain, so this guard would not be testing Ingest")

	assertEdgeChainRan(t, f, "ingest", path)
}
