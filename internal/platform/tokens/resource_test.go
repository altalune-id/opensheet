package tokens_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
