// Package api wires the Connect-RPC handlers into an http.Handler.
package api

import (
	"net/http"

	"connectrpc.com/connect"

	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	todov1connect "altalune.id/opensheet/gen/go/todo/v1/todov1connect"
	"altalune.id/opensheet/internal/api/interceptor"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/user"
)

// Server holds the wired Connect handlers and their runtime configuration.
type Server struct {
	Cfg    *config.Config
	Kernel *platform.Kernel
	Authn  authn.Chain

	Auths        *auth.Service
	Users        *user.Service
	Orgs         *org.Service
	Projects     *project.Service
	Todos        *todo.Service
	Invites      *invite.Service
	Credentials  *credential.Service
	Spreadsheets *spreadsheet.Service
	Sheets       *sheet.Service
	APIKeys      *apikey.Service

	AuthSvc *AuthService
	TodoSvc *TodoService

	OpenAPIEnabled   bool
	OpenAPIBasicAuth *BasicAuth
}

// New builds a Server from every domain service.
// Deps bundles everything a Server needs. A struct rather than positional
// parameters: the fields are mostly same-typed pointers, so a transposed pair
// would compile silently.
type Deps struct {
	Cfg    *config.Config
	Kernel *platform.Kernel
	Authn  authn.Chain

	Auths        *auth.Service
	Users        *user.Service
	Orgs         *org.Service
	Projects     *project.Service
	Todos        *todo.Service
	Invites      *invite.Service
	Credentials  *credential.Service
	Spreadsheets *spreadsheet.Service
	Sheets       *sheet.Service
	APIKeys      *apikey.Service

	TodoStore todo.Store
}

// New builds a Server from d.
func New(d Deps) *Server {
	s := &Server{
		Cfg:          d.Cfg,
		Kernel:       d.Kernel,
		Authn:        d.Authn,
		Auths:        d.Auths,
		Users:        d.Users,
		Orgs:         d.Orgs,
		Projects:     d.Projects,
		Todos:        d.Todos,
		Invites:      d.Invites,
		Credentials:  d.Credentials,
		Spreadsheets: d.Spreadsheets,
		Sheets:       d.Sheets,
		APIKeys:      d.APIKeys,
		AuthSvc:      NewAuthService(d.Orgs),
		TodoSvc:      NewTodoService(d.Todos, d.TodoStore, d.Projects),
	}
	if d.Cfg != nil {
		s.OpenAPIEnabled = d.Cfg.API.OpenAPI.Enabled
		if d.Cfg.API.OpenAPI.RequireBasicAuth {
			s.OpenAPIBasicAuth = &BasicAuth{
				User:     d.Cfg.API.OpenAPI.BasicAuthUser,
				Password: d.Cfg.API.OpenAPI.BasicAuthPassword,
			}
		}
	}
	return s
}

var (
	_ authv1connect.AuthServiceHandler = (*AuthService)(nil)
	_ todov1connect.TodoServiceHandler = (*TodoService)(nil)
)

// Handler mounts the Connect handlers plus OpenAPI endpoints under basePath+"/api".
func (s *Server) Handler(basePath string) http.Handler {
	opts := s.handlerOptions()

	inner := http.NewServeMux()
	todoPath, todoHandler := todov1connect.NewTodoServiceHandler(s.TodoSvc, opts...)
	inner.Handle(todoPath, todoHandler)
	authPath, authHandler := authv1connect.NewAuthServiceHandler(s.AuthSvc, opts...)
	inner.Handle(authPath, authHandler)

	if s.OpenAPIEnabled {
		yamlBody, jsonBody := openAPI()
		if len(yamlBody) > 0 {
			guard := openAPIGuard(s.OpenAPIBasicAuth)
			inner.Handle("/openapi.yaml", guard(openAPIHandler(yamlBody, "application/yaml")))
			inner.Handle("/openapi.json", guard(openAPIHandler(jsonBody, "application/json")))
			inner.Handle("/docs", guard(docsHandler(basePath+"/api/openapi.yaml")))
		}
	}

	mount := basePath + "/api"
	outer := http.NewServeMux()
	outer.Handle(mount+"/", http.StripPrefix(mount, inner))
	return outer
}

func (s *Server) handlerOptions() []connect.HandlerOption {
	ics := []connect.Interceptor{
		interceptor.RequestID(),
	}
	if otel, err := interceptor.OTel(nil, nil); err == nil && otel != nil {
		ics = append(ics, otel)
	}
	ics = append(ics,
		interceptor.Wrap(s.unexpected()),
		authn.Interceptor(s.Authn),
		interceptor.Tenant(),
	)
	return []connect.HandlerOption{connect.WithInterceptors(ics...)}
}

func (s *Server) unexpected() apperror.UnexpectedFunc {
	if s.Kernel == nil || s.Kernel.Reporter == nil {
		return nil
	}
	return s.Kernel.Reporter.Unexpected
}
