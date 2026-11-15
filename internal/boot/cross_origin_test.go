package boot_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
)

func crossSite(req *http.Request) *http.Request {
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example.com")
	return req
}

// TestConsole_RefusesCrossOriginPosts pins the CSRF guard on the console chain: SameSite=Lax does not cover a sibling subdomain.
func TestConsole_RefusesCrossOriginPosts(t *testing.T) {
	srv, err := boot.BootServer(context.Background(), newSmokeCfg(t), boot.WithLogger(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	serve := func(req *http.Request) int {
		rec := httptest.NewRecorder()
		srv.Web.ServeHTTP(rec, req)
		return rec.Code
	}
	post := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}

	require.Equal(t, http.StatusForbidden, serve(crossSite(post())), "a cross-site form post reached the console")

	sibling := post()
	sibling.Header.Set("Sec-Fetch-Site", "same-site")
	sibling.Header.Set("Origin", "https://other.altalune.id")
	require.Equal(t, http.StatusForbidden, serve(sibling), "a form posted from a sibling subdomain reached the console")

	untrusted := post()
	untrusted.Header.Set("Origin", "https://other.example.com")
	require.Equal(t, http.StatusForbidden, serve(untrusted), "an Origin that is neither the Host nor http.baseURL reached the console")

	sameOrigin := post()
	sameOrigin.Header.Set("Sec-Fetch-Site", "same-origin")
	require.NotEqual(t, http.StatusForbidden, serve(sameOrigin), "a same-origin post was refused")

	behindProxy := post()
	behindProxy.Header.Set("Origin", "http://127.0.0.1")
	require.NotEqual(t, http.StatusForbidden, serve(behindProxy), "the http.baseURL origin was refused although Host differs behind a proxy")

	require.NotEqual(t, http.StatusForbidden, serve(crossSite(httptest.NewRequest(http.MethodGet, "/login", nil))), "a cross-site GET must still load")
}

// TestCrossOriginGuard_LeavesBearerSurfacesAlone pins that /api, /mcp and the sheet data plane answer a cross-site bearer call as they answer any other.
func TestCrossOriginGuard_LeavesBearerSurfacesAlone(t *testing.T) {
	mcp := newMCPFixture(t, mcpOpts{enabled: true})
	dp := newDataplaneFixture(t)

	cases := []struct {
		name string
		srv  *boot.Server
		req  func() *http.Request
	}{
		{"control plane", mcp.srv, func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, projectListProcedure, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+mcp.readKey)
			return req
		}},
		{"mcp", mcp.srv, func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", mcpProtocolHeader)
			req.Header.Set("Authorization", "Bearer "+mcp.readKey)
			return req
		}},
		{"data plane", dp.srv, func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, dp.sheetURL, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+dp.readKey)
			return req
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain, cross := httptest.NewRecorder(), httptest.NewRecorder()
			tc.srv.Web.ServeHTTP(plain, tc.req())
			tc.srv.Web.ServeHTTP(cross, crossSite(tc.req()))
			require.NotEqual(t, http.StatusForbidden, plain.Code, "the plain call must pass, or this guard proves nothing; body=%s", plain.Body.String())
			require.Equal(t, plain.Code, cross.Code, "the cross-origin guard ran on a bearer surface; body=%s", cross.Body.String())
		})
	}
}
