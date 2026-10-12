package mcp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
)

type metadataDoc struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

func serveMetadata(t *testing.T, h http.Handler) (*httptest.ResponseRecorder, metadataDoc) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var doc metadataDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v (body %q)", err, rec.Body.String())
	}
	return rec, doc
}

func TestMetadataHandlerServesRFC9728Shape(t *testing.T) {
	h := mcpinternal.MetadataHandler(
		"https://app.example.com/mcp",
		[]string{"https://issuer.example.com"},
		[]string{"sheets:read", "sheets:write"},
	)
	rec, doc := serveMetadata(t, h)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if doc.Resource != "https://app.example.com/mcp" {
		t.Fatalf("resource = %q", doc.Resource)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://issuer.example.com" {
		t.Fatalf("authorization_servers = %v", doc.AuthorizationServers)
	}
	if len(doc.ScopesSupported) != 2 {
		t.Fatalf("scopes_supported = %v", doc.ScopesSupported)
	}
	if len(doc.BearerMethodsSupported) != 1 || doc.BearerMethodsSupported[0] != "header" {
		t.Fatalf("bearer_methods_supported = %v, want [header]", doc.BearerMethodsSupported)
	}
}

func TestMetadataHandlerIsUnauthenticated(t *testing.T) {
	h := mcpinternal.MetadataHandler("https://app.example.com/mcp", nil, nil)
	rec, _ := serveMetadata(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata must be servable with no credential; status = %d", rec.Code)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("metadata set a cookie: %q", got)
	}
}

func TestMetadataHandlerOmitsEmptyOptionalMembers(t *testing.T) {
	h := mcpinternal.MetadataHandler("https://app.example.com/mcp", nil, nil)
	rec, _ := serveMetadata(t, h)
	for _, member := range []string{"authorization_servers", "scopes_supported"} {
		if strings.Contains(rec.Body.String(), member) {
			t.Fatalf("body carries empty %q: %s", member, rec.Body.String())
		}
	}
}

func TestNewSurfaceInsertsWellKnownBeforeThePathComponent(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		wantPath string
		wantURL  string
	}{
		{
			name:     "no base path",
			resource: "https://app.example.com/mcp",
			wantPath: "/.well-known/oauth-protected-resource/mcp",
			wantURL:  "https://app.example.com/.well-known/oauth-protected-resource/mcp",
		},
		{
			name:     "non-empty base path",
			resource: "https://app.example.com/console/mcp",
			wantPath: "/.well-known/oauth-protected-resource/console/mcp",
			wantURL:  "https://app.example.com/.well-known/oauth-protected-resource/console/mcp",
		},
		{
			name:     "deep base path",
			resource: "https://app.example.com/a/b/c/mcp",
			wantPath: "/.well-known/oauth-protected-resource/a/b/c/mcp",
			wantURL:  "https://app.example.com/.well-known/oauth-protected-resource/a/b/c/mcp",
		},
		{
			name:     "no path component",
			resource: "https://app.example.com",
			wantPath: "/.well-known/oauth-protected-resource",
			wantURL:  "https://app.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:     "terminating slash is removed",
			resource: "https://app.example.com/",
			wantPath: "/.well-known/oauth-protected-resource",
			wantURL:  "https://app.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:     "terminating slash after a path is removed",
			resource: "https://app.example.com/console/mcp/",
			wantPath: "/.well-known/oauth-protected-resource/console/mcp",
			wantURL:  "https://app.example.com/.well-known/oauth-protected-resource/console/mcp",
		},
		{
			name:     "port is preserved",
			resource: "http://127.0.0.1:8080/mcp",
			wantPath: "/.well-known/oauth-protected-resource/mcp",
			wantURL:  "http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := mcpinternal.NewSurface(tt.resource)
			if err != nil {
				t.Fatalf("NewSurface(%q) = %v", tt.resource, err)
			}
			if s.Resource != tt.resource {
				t.Fatalf("Resource = %q, want %q verbatim", s.Resource, tt.resource)
			}
			if s.MetadataPath != tt.wantPath {
				t.Fatalf("MetadataPath = %q, want %q", s.MetadataPath, tt.wantPath)
			}
			if s.MetadataURL != tt.wantURL {
				t.Fatalf("MetadataURL = %q, want %q", s.MetadataURL, tt.wantURL)
			}
			if strings.HasPrefix(s.MetadataPath, "/console") || strings.HasPrefix(s.MetadataPath, "/a/") {
				t.Fatalf("MetadataPath %q appends the base path instead of inserting the well-known URI", s.MetadataPath)
			}
		})
	}
}

func TestNewSurfaceRejectsAnUnusableResource(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		reason   string
	}{
		{name: "empty", resource: "", reason: "not absolute"},
		{name: "relative", resource: "/mcp", reason: "not absolute"},
		{name: "scheme only", resource: "https://", reason: "not absolute"},
		{name: "fragment", resource: "https://app.example.com/mcp#f", reason: "carrying a fragment"},
		{name: "unparseable", resource: "https://app.example.com/%zz", reason: "not a URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mcpinternal.NewSurface(tt.resource)
			if err == nil {
				t.Fatalf("NewSurface(%q) = nil error", tt.resource)
			}
			if !mcpinternal.IsResourceInvalidError(err) {
				t.Fatalf("IsResourceInvalidError(%v) = false", err)
			}
			if !strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("error %q does not name %q", err, tt.reason)
			}
		})
	}
}

func mcpConfig(t *testing.T, basePath string) *config.Config {
	t.Helper()
	c := &config.Config{
		Mode:     config.ModeSelfhosted,
		DB:       db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:"},
		Genesis:  config.GenesisConfig{Email: "root@example.com", Password: "x"},
		Security: config.SecurityConfig{EncryptionKey: strings.Repeat("ab", 32)},
	}
	c.Tenant.SingletonOrg.Slug = "default"
	c.Tenant.SingletonOrg.Name = "Default Organization"
	c.HTTP.BaseURL = "https://app.example.com"
	c.HTTP.BasePath = basePath
	c.Tokens.Issuer = "https://issuer.example.com"
	c.MCP.Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return c
}

// TestMetadataChallengeAndVerifierAudienceAgree pins the three values an MCP host round-trips between: the `resource` the metadata document advertises, the audience the token verifier enforces, and the metadata URL the 401 challenge names.
func TestMetadataChallengeAndVerifierAudienceAgree(t *testing.T) {
	for _, basePath := range []string{"", "/console", "/a/b"} {
		t.Run("basePath="+basePath, func(t *testing.T) {
			cfg := mcpConfig(t, basePath)

			wantResource := "https://app.example.com" + basePath + "/mcp"
			wantMetadataURL := "https://app.example.com" + mcpinternal.WellKnownPrefix + basePath + "/mcp"
			wantChallenge := fmt.Sprintf("Bearer resource_metadata=%q", wantMetadataURL)

			if cfg.MCP.Audience != wantResource {
				t.Fatalf("mcp.audience = %q, want %q", cfg.MCP.Audience, wantResource)
			}
			surface, err := mcpinternal.NewSurface(cfg.MCP.Audience)
			if err != nil {
				t.Fatalf("NewSurface(%q): %v", cfg.MCP.Audience, err)
			}

			verifierCfg := cfg.Tokens
			verifierCfg.Audience = surface.Resource

			mux := http.NewServeMux()
			mux.Handle("GET "+surface.MetadataPath, mcpinternal.MetadataHandler(surface.Resource, []string{cfg.Tokens.Issuer}, []string{"sheets:read"}))
			mux.Handle(surface.MetadataPath+"/", http.NotFoundHandler())

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, wantMetadataURL, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 — the document is not served where the challenge points", wantMetadataURL, rec.Code)
			}
			var doc metadataDoc
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			if doc.Resource != verifierCfg.Audience {
				t.Fatalf("document resource %q != verifier audience %q", doc.Resource, verifierCfg.Audience)
			}
			if doc.Resource != wantResource {
				t.Fatalf("document resource %q != mounted endpoint %q", doc.Resource, wantResource)
			}

			guarded := mcpinternal.Authenticate(nil, authn.Scheme{Prefix: "key_"}, surface.MetadataURL, nil)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
			)
			denied := httptest.NewRecorder()
			guarded.ServeHTTP(denied, httptest.NewRequestWithContext(t.Context(), http.MethodPost, surface.Resource, nil))
			if denied.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s = %d, want 401", surface.Resource, denied.Code)
			}
			if got := denied.Header().Get("WWW-Authenticate"); got != wantChallenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, wantChallenge)
			}
		})
	}
}
