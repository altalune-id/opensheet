package tokens_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/platform/tokens"
)

func TestNewResourceInsertsWellKnownBeforeThePath(t *testing.T) {
	tests := []struct {
		resource, wantPath, wantURL string
	}{
		{"https://app.example.com/mcp", "/.well-known/oauth-protected-resource/mcp", "https://app.example.com/.well-known/oauth-protected-resource/mcp"},
		{"https://app.example.com/api", "/.well-known/oauth-protected-resource/api", "https://app.example.com/.well-known/oauth-protected-resource/api"},
		{"https://app.example.com/console/api", "/.well-known/oauth-protected-resource/console/api", "https://app.example.com/.well-known/oauth-protected-resource/console/api"},
		{"https://app.example.com/console/mcp/", "/.well-known/oauth-protected-resource/console/mcp", "https://app.example.com/.well-known/oauth-protected-resource/console/mcp"},
		{"http://127.0.0.1:8080/mcp", "/.well-known/oauth-protected-resource/mcp", "http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			r, err := tokens.NewResource(tt.resource)
			if err != nil {
				t.Fatal(err)
			}
			if r.Identifier != tt.resource || r.MetadataPath != tt.wantPath || r.MetadataURL != tt.wantURL {
				t.Fatalf("NewResource(%q) = %+v", tt.resource, r)
			}
		})
	}
}

func TestNewResourceRejectsAnUnusableIdentifier(t *testing.T) {
	for _, raw := range []string{"", "/mcp", "https://", "https://app.example.com/mcp#f", "urn:opensheet:api"} {
		if _, err := tokens.NewResource(raw); !tokens.IsResourceInvalidError(err) {
			t.Fatalf("NewResource(%q) = %v, want *ResourceInvalidError", raw, err)
		}
	}
}

func TestResourceMetadataAndChallengesAgree(t *testing.T) {
	r, err := tokens.NewResource("https://app.example.com/api")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	r.MetadataHandler("https://idp.example.com/p/app", []string{"posts:read"}).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, r.MetadataURL, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
		BearerMethods        []string `json:"bearer_methods_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Resource != r.Identifier || len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://idp.example.com/p/app" ||
		len(doc.ScopesSupported) != 1 || len(doc.BearerMethods) != 1 || doc.BearerMethods[0] != "header" {
		t.Fatalf("document = %+v", doc)
	}
	if got, want := r.Challenge(), `Bearer resource_metadata="`+r.MetadataURL+`"`; got != want {
		t.Fatalf("Challenge = %q, want %q", got, want)
	}
	if got, want := r.InvalidToken(), `Bearer resource_metadata="`+r.MetadataURL+`", error="invalid_token"`; got != want {
		t.Fatalf("InvalidToken = %q, want %q", got, want)
	}
	want := `Bearer resource_metadata="` + r.MetadataURL + `", error="insufficient_scope", scope="posts:write"`
	if got := r.InsufficientScope("posts:write"); got != want {
		t.Fatalf("InsufficientScope = %q, want %q", got, want)
	}
}

func TestCLIMetadataHandlerAddsTheCLIClientID(t *testing.T) {
	r, err := tokens.NewResource("https://app.example.com/api")
	if err != nil {
		t.Fatal(err)
	}
	serve := func(cliClientID, method string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.CLIMetadataHandler("https://idp.example.com/p/app", []string{"apikeys:read"}, cliClientID).
			ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, r.MetadataURL, nil))
		return rec
	}

	rec := serve("cli-client", http.MethodGet)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status = %d, content type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc[tokens.CLIClientIDMember] != "cli-client" || doc["resource"] != r.Identifier || doc["bearer_methods_supported"] == nil {
		t.Fatalf("document = %v", doc)
	}

	plain := serve("", http.MethodGet)
	if strings.Contains(plain.Body.String(), tokens.CLIClientIDMember) {
		t.Fatalf("no CLI client configured, yet the document names one: %s", plain.Body.String())
	}
	if got := serve("cli-client", http.MethodPost); got.Code != http.StatusMethodNotAllowed || got.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST status = %d, Allow = %q", got.Code, got.Header().Get("Allow"))
	}
}
