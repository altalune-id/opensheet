package mcp_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/opensheet/mcp"
)

func loopbackPost(host string) *http.Request {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", strings.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5150}
	return req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(local)))
}

// SECURITY: a same-host proxy may pass the app's own public host on a loopback connection; any other public host is still DNS rebinding.
func TestHandler_AllowedHostsOnLoopback(t *testing.T) {
	allowed := mcp.NewServer(mcp.WithAllowedHosts("id.example.com")).Handler()
	sdkDefault := mcp.NewServer().Handler()
	for name, tc := range map[string]struct {
		h      http.Handler
		host   string
		status int
	}{
		"the public host":                      {allowed, "ID.Example.com:443", http.StatusOK},
		"another public host":                  {allowed, "evil.example", http.StatusForbidden},
		"a loopback host":                      {allowed, "127.0.0.1:5150", http.StatusOK},
		"localhost":                            {allowed, "localhost:5150", http.StatusOK},
		"no allowed hosts keeps the SDK check": {sdkDefault, "id.example.com", http.StatusForbidden},
		"the SDK allows a loopback host":       {sdkDefault, "localhost", http.StatusOK},
	} {
		rec := httptest.NewRecorder()
		tc.h.ServeHTTP(rec, loopbackPost(tc.host))
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tc.status, rec.Body.String())
		}
		if tc.status == http.StatusForbidden && !strings.Contains(rec.Body.String(), "invalid Host header") {
			t.Errorf("%s: body %q", name, rec.Body.String())
		}
	}
}

func TestHandler_AllowedHostsOffLoopback(t *testing.T) {
	h := mcp.NewServer(mcp.WithAllowedHosts("id.example.com", "")).Handler()
	req := loopbackPost("evil.example")
	public := &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 443}
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(public)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a public listener is not DNS rebinding: %d %s", rec.Code, rec.Body.String())
	}
}
