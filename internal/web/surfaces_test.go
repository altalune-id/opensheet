package web_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/web"
)

func marker(name string, hit *[]string) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*hit = append(*hit, name)
			next.ServeHTTP(w, r)
		})
	}
}

func TestProbesSkipSSRChain(t *testing.T) {
	var hit []string
	h := web.NewServer(web.ServerOpts{
		BasePath: "/app",
		Chains: web.SurfaceChains{
			Console: []web.Middleware{marker("console", &hit)},
			Probes:  []web.Middleware{marker("probes", &hit)},
		},
	})

	for _, path := range []string{"/healthz", "/readyz", "/robots.txt"} {
		t.Run(path, func(t *testing.T) {
			hit = nil
			req := httptest.NewRequest(http.MethodGet, path, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
			for _, got := range hit {
				if got == "console" {
					t.Fatalf("%s ran the console chain; probes must run edge only", path)
				}
			}
			if !slices.Contains(hit, "probes") {
				t.Fatalf("%s did not run the probes chain: hit=%v", path, hit)
			}
		})
	}
}

func echo(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(name))
	})
}

func TestMachineSurfacesSkipSSRChain(t *testing.T) {
	var hit []string
	h := web.NewServer(web.ServerOpts{
		BasePath:      "/app",
		APIHandler:    echo("control"),
		DataHandler:   echo("data"),
		IngestHandler: echo("ingest"),
		MCPHandler:    echo("mcp"),
		Chains: web.SurfaceChains{
			Console: []web.Middleware{marker("console", &hit)},
			Control: []web.Middleware{marker("control", &hit)},
			Data:    []web.Middleware{marker("data", &hit)},
			Ingest:  []web.Middleware{marker("ingest", &hit)},
			MCP:     []web.Middleware{marker("mcp", &hit)},
		},
	})

	tests := []struct{ name, path, want string }{
		{"control plane", "/app/api/todo.v1.TodoService/List", "control"},
		{"data plane", "/app/api/v1/orgs/acme/projects/main/posts", "data"},
		{"ingest", "/app/hooks/stripe", "ingest"},
		{"mcp", "/app/mcp", "mcp"},
		{"mcp subtree", "/app/mcp/messages", "mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit = nil
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
			for _, got := range hit {
				if got == "console" {
					t.Fatalf("%s ran the console chain", tt.path)
				}
			}
			if len(hit) != 1 || hit[0] != tt.want {
				t.Fatalf("chain = %v, want [%s]", hit, tt.want)
			}
		})
	}
}

func TestUnmatchedPathUnderBasePathRunsConsoleChain(t *testing.T) {
	var hit []string
	h := web.NewServer(web.ServerOpts{
		BasePath: "/app",
		Chains: web.SurfaceChains{
			Console: []web.Middleware{marker("console", &hit)},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/nowhere", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !slices.Contains(hit, "console") {
		t.Fatalf("unmatched path did not run the console chain: hit=%v", hit)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMountPrefixesReserved(t *testing.T) {
	bases := []struct{ name, basePath string }{
		{"root base path", ""},
		{"nested base path", "/app"},
	}
	for _, base := range bases {
		t.Run(base.name, func(t *testing.T) {
			h := web.NewServer(web.ServerOpts{
				BasePath:      base.basePath,
				APIHandler:    echo("control"),
				DataHandler:   echo("data"),
				IngestHandler: echo("ingest"),
				MCPHandler:    echo("mcp"),
			})

			tests := []struct{ sub, want string }{
				{"/api/todo.v1.TodoService/List", "control"},
				{"/api/v1/orgs/acme/projects/main/posts", "data"},
				{"/hooks/stripe", "ingest"},
				{"/mcp", "mcp"},
				{"/mcp/", "mcp"},
				{"/mcp/messages", "mcp"},
			}
			for _, tt := range tests {
				t.Run(tt.sub, func(t *testing.T) {
					path := base.basePath + tt.sub
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
					if got := rec.Body.String(); got != tt.want {
						t.Fatalf("%s resolved to %q, want %q", path, got, tt.want)
					}
				})
			}
		})
	}
}

func TestMCPMountDoesNotSwallowNeighbouringPaths(t *testing.T) {
	h := web.NewServer(web.ServerOpts{
		BasePath:      "/app",
		APIHandler:    echo("control"),
		DataHandler:   echo("data"),
		IngestHandler: echo("ingest"),
		MCPHandler:    echo("mcp"),
	})

	for _, path := range []string{"/app/mcpx", "/app/mcp-tools", "/app/dashboard"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if got := rec.Body.String(); got == "mcp" {
				t.Fatalf("%s resolved to the mcp handler; /mcp must not swallow neighbouring paths", path)
			}
		})
	}
}

func TestMCPSurfaceSkipsSSRChain(t *testing.T) {
	var hit []string
	h := web.NewServer(web.ServerOpts{
		BasePath:   "/app",
		MCPHandler: echo("mcp"),
		Chains: web.SurfaceChains{
			Console: []web.Middleware{marker("console", &hit)},
			MCP:     []web.Middleware{marker("mcp", &hit)},
		},
	})

	for _, path := range []string{"/app/mcp", "/app/mcp/messages"} {
		t.Run(path, func(t *testing.T) {
			hit = nil
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			if got := rec.Body.String(); got != "mcp" {
				t.Fatalf("%s resolved to %q, want the mcp handler", path, got)
			}
			if slices.Contains(hit, "console") {
				t.Fatalf("%s ran the console chain; MCP must never sit behind CSP/Session/Tenant/i18n/OnboardingGate/WelcomeGate: hit=%v", path, hit)
			}
			if len(hit) != 1 || hit[0] != "mcp" {
				t.Fatalf("%s chain = %v, want [mcp]", path, hit)
			}
		})
	}
}

func TestMCPMetadataStaysRootAnchoredUnderBasePath(t *testing.T) {
	const metadataPath = "/.well-known/oauth-protected-resource/app/mcp"

	var hit []string
	h := web.NewServer(web.ServerOpts{
		BasePath:           "/app",
		MCPHandler:         echo("mcp"),
		MCPMetadataHandler: echo("metadata"),
		MCPMetadataPath:    metadataPath,
		Chains: web.SurfaceChains{
			Console: []web.Middleware{marker("console", &hit)},
			MCP:     []web.Middleware{marker("mcp", &hit)},
			Probes:  []web.Middleware{marker("probes", &hit)},
		},
	})

	t.Run("served at the root anchored well-known path", func(t *testing.T) {
		hit = nil
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metadataPath, nil))
		if got := rec.Body.String(); got != "metadata" {
			t.Fatalf("%s resolved to %q, want the metadata handler", metadataPath, got)
		}
		if len(hit) != 1 || hit[0] != "probes" {
			t.Fatalf("%s chain = %v, want [probes]", metadataPath, hit)
		}
	})

	t.Run("not mounted under the base path", func(t *testing.T) {
		hit = nil
		path := "/app" + metadataPath
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Body.String(); strings.Contains(got, "metadata") {
			t.Fatalf("%s resolved to the metadata handler; RFC 9728 anchors it at the root", path)
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
}
