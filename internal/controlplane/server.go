// Package controlplane wires the Connect-RPC handlers into an http.Handler.
package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	apikeyv1connect "altalune.id/opensheet/gen/go/apikey/v1/apikeyv1connect"
	authv1 "altalune.id/opensheet/gen/go/auth/v1"
	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	credentialv1connect "altalune.id/opensheet/gen/go/credential/v1/credentialv1connect"
	orgv1 "altalune.id/opensheet/gen/go/org/v1"
	orgv1connect "altalune.id/opensheet/gen/go/org/v1/orgv1connect"
	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	projectv1connect "altalune.id/opensheet/gen/go/project/v1/projectv1connect"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	sheetv1connect "altalune.id/opensheet/gen/go/sheet/v1/sheetv1connect"
	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	spreadsheetv1connect "altalune.id/opensheet/gen/go/spreadsheet/v1/spreadsheetv1connect"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/controlplane/interceptor"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/user"
)

// Deps bundles the domain services a Server mounts.
type Deps struct {
	Auths    *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Invites  *invite.Service

	Credentials       *credential.Service
	CredentialConnect *credential.ConnectWorkflow
	Spreadsheets      *spreadsheet.Service
	Sheets            *sheet.Service
}

// Server holds the wired Connect handlers and their runtime configuration.
type Server struct {
	Cfg    *config.Config
	Kernel *platform.Kernel

	Auths    *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Invites  *invite.Service

	// APIKeys is set by boot after New returns, mirroring Authn below.
	APIKeys *apikey.Service

	AuthSvc    *AuthService
	ProjectSvc *ProjectService
	MemberSvc  *MemberService
	APIKeySvc  *APIKeyService

	CredentialSvc  *CredentialService
	SpreadsheetSvc *SpreadsheetService
	SheetSvc       *SheetService

	Authn authn.Chain
	// Resource is the control plane's protected resource, named in every 401 and 403 challenge; nil sends none.
	Resource *tokens.Resource

	OpenAPIEnabled   bool
	OpenAPIBasicAuth *BasicAuth
}

// New builds a Server from every domain service.
func New(cfg *config.Config, kernel *platform.Kernel, d Deps) *Server {
	s := &Server{
		Cfg:      cfg,
		Kernel:   kernel,
		Auths:    d.Auths,
		Users:    d.Users,
		Orgs:     d.Orgs,
		Projects: d.Projects,
		Invites:  d.Invites,

		AuthSvc:    NewAuthService(d.Orgs),
		ProjectSvc: NewProjectService(d.Projects),
		MemberSvc:  NewMemberService(d.Orgs),

		CredentialSvc:  NewCredentialService(d.Credentials, d.CredentialConnect, d.Projects),
		SpreadsheetSvc: NewSpreadsheetService(d.Spreadsheets, d.Projects),
		SheetSvc:       NewSheetService(d.Sheets, d.Projects),
	}
	if cfg != nil {
		s.OpenAPIEnabled = cfg.API.OpenAPI.Enabled
		if cfg.API.OpenAPI.RequireBasicAuth {
			s.OpenAPIBasicAuth = &BasicAuth{
				User:     cfg.API.OpenAPI.BasicAuthUser,
				Password: cfg.API.OpenAPI.BasicAuthPassword,
			}
		}
	}
	return s
}

var (
	_ authv1connect.AuthServiceHandler       = (*AuthService)(nil)
	_ projectv1connect.ProjectServiceHandler = (*ProjectService)(nil)
	_ orgv1connect.MemberServiceHandler      = (*MemberService)(nil)
	_ apikeyv1connect.APIKeyServiceHandler   = (*APIKeyService)(nil)

	_ credentialv1connect.CredentialServiceHandler   = (*CredentialService)(nil)
	_ spreadsheetv1connect.SpreadsheetServiceHandler = (*SpreadsheetService)(nil)
	_ sheetv1connect.SheetServiceHandler             = (*SheetService)(nil)
)

// Handler mounts the Connect handlers plus OpenAPI endpoints under basePath+"/api".
func (s *Server) Handler(basePath string) http.Handler {
	opts := s.handlerOptions()
	// NOTE: built here, not in New, since s.APIKeys isn't set until after New returns.
	s.APIKeySvc = NewAPIKeyService(s.APIKeys, s.Projects)

	inner := http.NewServeMux()
	for _, m := range []mountedHandler{
		mounted(authv1connect.NewAuthServiceHandler(s.AuthSvc, opts...)),
		mounted(projectv1connect.NewProjectServiceHandler(s.ProjectSvc, opts...)),
		mounted(orgv1connect.NewMemberServiceHandler(s.MemberSvc, opts...)),
		mounted(apikeyv1connect.NewAPIKeyServiceHandler(s.APIKeySvc, opts...)),
		mounted(credentialv1connect.NewCredentialServiceHandler(s.CredentialSvc, opts...)),
		mounted(spreadsheetv1connect.NewSpreadsheetServiceHandler(s.SpreadsheetSvc, opts...)),
		mounted(sheetv1connect.NewSheetServiceHandler(s.SheetSvc, opts...)),
	} {
		inner.Handle(m.path, m.handler)
	}

	if s.OpenAPIEnabled {
		yamlBody, jsonBody := openAPI()
		if len(yamlBody) > 0 {
			guard := openAPIGuard(s.OpenAPIBasicAuth)
			inner.Handle("/openapi.yaml", s.recoverHTTP(guard(openAPIHandler(yamlBody, "application/yaml"))))
			inner.Handle("/openapi.json", s.recoverHTTP(guard(openAPIHandler(jsonBody, "application/json"))))
			inner.Handle("/docs", s.recoverHTTP(guard(docsHandler(basePath+"/api/openapi.yaml"))))
		}
	}

	mount := basePath + "/api"
	outer := http.NewServeMux()
	outer.Handle(mount+"/", http.StripPrefix(mount, inner))
	return outer
}

// MountedProcedures returns every Connect procedure path the handler serves.
func (s *Server) MountedProcedures() []string {
	return slices.Concat(
		serviceProcedures(authv1.File_auth_v1_auth_proto, "AuthService"),
		serviceProcedures(projectv1.File_project_v1_project_proto, "ProjectService"),
		serviceProcedures(orgv1.File_org_v1_org_proto, "MemberService"),
		serviceProcedures(apikeyv1.File_apikey_v1_apikey_proto, "APIKeyService"),
		serviceProcedures(credentialv1.File_credential_v1_credential_proto, "CredentialService"),
		serviceProcedures(spreadsheetv1.File_spreadsheet_v1_spreadsheet_proto, "SpreadsheetService"),
		serviceProcedures(sheetv1.File_sheet_v1_sheet_proto, "SheetService"),
	)
}

type mountedHandler struct {
	path    string
	handler http.Handler
}

func mounted(path string, handler http.Handler) mountedHandler {
	return mountedHandler{path: path, handler: handler}
}

func serviceProcedures(file protoreflect.FileDescriptor, name string) []string {
	svc := file.Services().ByName(protoreflect.Name(name))
	methods := svc.Methods()
	procedures := make([]string, methods.Len())
	for i := range methods.Len() {
		procedures[i] = fmt.Sprintf("/%s/%s", svc.FullName(), methods.Get(i).Name())
	}
	return procedures
}

func (s *Server) handlerOptions() []connect.HandlerOption {
	return s.handlerOptionsFor(ScopeTable())
}

func (s *Server) handlerOptionsFor(table authn.ScopeTable) []connect.HandlerOption {
	ics := []connect.Interceptor{
		interceptor.RequestID(),
	}
	if otel, err := interceptor.OTel(nil, nil); err == nil && otel != nil {
		ics = append(ics, otel)
	}
	ics = append(ics,
		interceptor.Wrap(s.unexpected()),
		translateAuthnErrors(s.Resource),
		authn.Interceptor(s.Authn, table),
		interceptor.Tenant(),
	)
	return []connect.HandlerOption{
		connect.WithRecover(s.recoverPanic),
		connect.WithInterceptors(ics...),
	}
}

func (s *Server) recoverPanic(ctx context.Context, _ connect.Spec, _ http.Header, p any) error {
	return interceptor.Translate(ctx, fmt.Errorf("panic: %v", p), s.unexpected())
}

func (s *Server) recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			err := fmt.Errorf("panic: %v", p)
			if unexpected := s.unexpected(); unexpected != nil {
				_ = unexpected(r.Context(), "api: unexpected", err)
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) unexpected() apperror.UnexpectedFunc {
	if s.Kernel == nil || s.Kernel.Reporter == nil {
		return nil
	}
	return s.Kernel.Reporter.Unexpected
}
