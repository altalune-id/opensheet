package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/session"
)

//go:embed all:static
var staticFS embed.FS

// StaticFS returns the fs.FS rooted at the static/ subdirectory.
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("web: static sub-fs: " + err.Error())
	}
	return sub
}

// Mux is the subset of *http.ServeMux a handler registers against.
type Mux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// Register registers each handler onto the given mux.
type Register interface{ Register(mux Mux) }

type recordingMux struct {
	mux      *http.ServeMux
	patterns []string
}

func (m *recordingMux) Handle(pattern string, handler http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.mux.Handle(pattern, handler)
}

func (m *recordingMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.mux.HandleFunc(pattern, handler)
}

// Middleware is the standard net/http middleware shape.
type Middleware = func(http.Handler) http.Handler

// SurfaceChains carries one middleware chain per surface.
type SurfaceChains struct {
	Console []Middleware
	Control []Middleware
	Data    []Middleware
	Ingest  []Middleware
	MCP     []Middleware
	Probes  []Middleware
}

func wrap(chain []Middleware, h http.Handler) http.Handler {
	for _, mw := range slices.Backward(chain) {
		h = mw(h)
	}
	return h
}

// ServerOpts bundles the pieces NewServer assembles.
type ServerOpts struct {
	AppHandlers        []Register
	APIHandler         http.Handler
	DataHandler        http.Handler
	IngestHandler      http.Handler
	MCPHandler         http.Handler
	MCPMetadataHandler http.Handler
	MCPMetadataPath    string
	MCPChallengeRoutes map[string]http.Handler
	RobotsCfg          *robotsConfig
	BasePath           string
	HealthOK           func() bool
	Chains             SurfaceChains
	Logger             *slog.Logger
	SessionStore       session.Store
	Secret             []byte
	Reporter           apperror.UnexpectedFunc
}

type robotsConfig = struct{ RobotsTxt string }

// NewServer wires handlers, static, robots, healthz and API into one http.Handler.
func NewServer(o ServerOpts) http.Handler {
	h, _ := NewServerWithRoutes(o)
	return h
}

// NewServerWithRoutes is NewServer plus the app-route patterns the handlers registered.
func NewServerWithRoutes(o ServerOpts) (handler http.Handler, routes []string) { //nolint:nonamedreturns // two return values differ in role
	rec := &recordingMux{mux: http.NewServeMux()}
	for _, h := range o.AppHandlers {
		h.Register(rec)
	}
	app := rec.mux
	app.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(StaticFS()))))

	outer := http.NewServeMux()

	healthz := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	readyz := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if o.HealthOK != nil && !o.HealthOK() {
			http.Error(w, "unready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	outer.Handle("GET /healthz", wrap(o.Chains.Probes, healthz))
	outer.Handle("GET /readyz", wrap(o.Chains.Probes, readyz))
	outer.Handle("GET /robots.txt", wrap(o.Chains.Probes, robotsFromServerOpts(o)))

	if o.APIHandler != nil {
		outer.Handle(Path(o.BasePath, "/api")+"/", wrap(o.Chains.Control, o.APIHandler))
	}
	// NOTE: ServeMux resolves /api/v1/ over /api/ by specificity, whatever the registration order.
	if o.DataHandler != nil {
		outer.Handle(Path(o.BasePath, "/api")+"/v1/", wrap(o.Chains.Data, o.DataHandler))
	}
	if o.IngestHandler != nil {
		outer.Handle(Path(o.BasePath, "/hooks")+"/", wrap(o.Chains.Ingest, o.IngestHandler))
	}
	// NOTE: RFC 9728 inserts the well-known URI between host and path, so the metadata document
	// sits outside BasePath even when BasePath is set.
	if o.MCPMetadataHandler != nil && o.MCPMetadataPath != "" {
		outer.Handle("GET "+o.MCPMetadataPath, wrap(o.Chains.Probes, o.MCPMetadataHandler))
	}
	for pattern, h := range o.MCPChallengeRoutes {
		outer.Handle(pattern, wrap(o.Chains.Probes, h))
	}
	if o.MCPHandler != nil {
		outer.Handle(Path(o.BasePath, "/mcp"), wrap(o.Chains.MCP, o.MCPHandler))
		outer.Handle(Path(o.BasePath, "/mcp")+"/", wrap(o.Chains.MCP, o.MCPHandler))
	}

	base := strings.TrimRight(o.BasePath, "/")
	if base == "" {
		outer.Handle("/", wrap(o.Chains.Console, app))
		return outer, rec.patterns
	}
	outer.Handle("/", wrap(o.Chains.Console, http.NotFoundHandler()))
	outer.Handle(base+"/", wrap(o.Chains.Console, http.StripPrefix(base, app)))
	outer.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/", http.StatusMovedPermanently)
	})
	return outer, rec.patterns
}

func robotsFromServerOpts(o ServerOpts) http.Handler {
	body := "User-agent: *\nDisallow: /\n"
	if o.RobotsCfg != nil && o.RobotsCfg.RobotsTxt != "" {
		body = o.RobotsCfg.RobotsTxt
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}
