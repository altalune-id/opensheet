package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// WellKnownPrefix is the RFC 9728 well-known URI, inserted between a resource identifier's host and its path. https://www.rfc-editor.org/rfc/rfc9728.html#section-3
const WellKnownPrefix = "/.well-known/oauth-protected-resource"

// ResourceInvalidError reports an MCP resource identifier that cannot address a metadata document.
type ResourceInvalidError struct {
	Resource string
	Reason   string
}

func (e *ResourceInvalidError) Error() string {
	return "mcp: resource identifier " + strconv.Quote(e.Resource) + " is " + e.Reason
}

// IsResourceInvalidError reports whether err is a *ResourceInvalidError.
func IsResourceInvalidError(err error) bool {
	var target *ResourceInvalidError
	return errors.As(err, &target)
}

// Surface carries the identifiers the MCP mount, its token verifier and its 401 challenge must all agree on. https://www.rfc-editor.org/rfc/rfc9728.html#section-3
type Surface struct {
	Resource     string
	MetadataPath string
	MetadataURL  string
}

// NewSurface derives the RFC 9728 metadata location from an MCP resource identifier.
func NewSurface(resource string) (Surface, error) {
	u, err := url.Parse(resource)
	if err != nil {
		return Surface{}, &ResourceInvalidError{Resource: resource, Reason: "not a URL"}
	}
	if !u.IsAbs() || u.Host == "" {
		return Surface{}, &ResourceInvalidError{Resource: resource, Reason: "not absolute"}
	}
	if u.Fragment != "" || strings.Contains(resource, "#") {
		return Surface{}, &ResourceInvalidError{Resource: resource, Reason: "carrying a fragment"}
	}
	path := WellKnownPrefix + strings.TrimSuffix(u.EscapedPath(), "/")
	suffix := path
	if u.RawQuery != "" {
		suffix += "?" + u.RawQuery
	}
	return Surface{
		Resource:     resource,
		MetadataPath: path,
		MetadataURL:  u.Scheme + "://" + u.Host + suffix,
	}, nil
}

type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
}

// MetadataHandler serves the RFC 9728 protected-resource metadata document. SECURITY: unauthenticated by design — a client reads it precisely because it holds no token yet — so the body carries only the deployment-wide identifiers RFC 9728 defines and never a tenant identifier, an internal hostname or a per-caller scope list.
func MetadataHandler(resource string, authorizationServers, scopesSupported []string) http.Handler {
	body, err := json.Marshal(protectedResourceMetadata{
		Resource:               resource,
		AuthorizationServers:   authorizationServers,
		ScopesSupported:        scopesSupported,
		BearerMethodsSupported: []string{"header"},
	})
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metadata unavailable", http.StatusInternalServerError)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	})
}
