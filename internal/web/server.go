package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/session"
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

// Register registers each handler onto the given mux.
type Register interface{ Register(mux *http.ServeMux) }

// Middleware is the standard net/http middleware shape.
type Middleware = func(http.Handler) http.Handler

// ServerOpts bundles the pieces NewServer assembles.
type ServerOpts struct {
	AppHandlers  []Register
	APIHandler   http.Handler
	RobotsCfg    *robotsConfig
	BasePath     string
	HealthOK     func() bool
	Middlewares  []Middleware
	Logger       *slog.Logger
	SessionStore session.Store
	Secret       []byte
	Reporter     apperror.UnexpectedFunc
}

type robotsConfig = struct{ RobotsTxt string }

// NewServer wires handlers, static, robots, healthz and API into one http.Handler.
func NewServer(o ServerOpts) http.Handler {
	app := http.NewServeMux()
	for _, h := range o.AppHandlers {
		h.Register(app)
	}
	app.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(StaticFS()))))

	outer := http.NewServeMux()

	outer.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	outer.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if o.HealthOK != nil && !o.HealthOK() {
			http.Error(w, "unready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	robots := robotsFromServerOpts(o)
	outer.Handle("GET /robots.txt", robots)

	if o.APIHandler != nil {
		outer.Handle(Path(o.BasePath, "/api")+"/", o.APIHandler)
	}

	base := strings.TrimRight(o.BasePath, "/")
	if base == "" {
		outer.Handle("/", app)
	} else {
		outer.Handle(base+"/", http.StripPrefix(base, app))
		outer.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, base+"/", http.StatusMovedPermanently)
		})
	}

	var handler http.Handler = outer
	for i := len(o.Middlewares) - 1; i >= 0; i-- {
		handler = o.Middlewares[i](handler)
	}
	return handler
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
