package boot

import (
	"context"
	"fmt"
	stdlog "log"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/dataplane"
	i18npkg "altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/ingest"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/web"
	webhandlers "altalune.id/opensheet/internal/web/handlers"
	webmw "altalune.id/opensheet/internal/web/middleware"
)

func buildAPIHandler(cfg *config.Config, k *platform.Kernel, s *Services, resource *tokens.Resource) (*controlplane.Server, http.Handler) {
	srv := controlplane.New(cfg, k, controlplane.Deps{
		Auths:    s.Auth,
		Users:    s.Users,
		Orgs:     s.Orgs,
		Projects: s.Projects,
		Invites:  s.Invites,

		Credentials:       s.Credentials,
		CredentialConnect: s.Connect,
		Spreadsheets:      s.Spreadsheets,
		Sheets:            s.Sheets,
	})
	srv.Authn = s.Authn
	srv.Resource = resource
	srv.APIKeys = s.APIKeys
	if !cfg.API.Enabled {
		return srv, nil
	}
	h := srv.Handler(cfg.HTTP.BasePath)
	return srv, h
}

func buildDataHandler(cfg *config.Config, caps capabilities.Capabilities, slogger *slog.Logger, s *Services) http.Handler {
	if !caps.DataPlaneEnabled {
		return nil
	}
	return dataplane.NewSheetHandler(dataplane.SheetHandlerParams{
		BasePath:   cfg.HTTP.BasePath,
		Orgs:       orgServiceForDataplane{svc: s.Orgs},
		Projects:   projectServiceForDataplane{svc: s.Projects},
		Sheets:     sheetsForData{svc: s.Sheets},
		Reader:     s.Read,
		Inspector:  s.Read,
		Purger:     s.Sheets,
		Writer:     s.Write,
		Tabs:       s.Spreadsheets,
		Authz:      s.KeyAuthn,
		Caps:       caps,
		DefaultTTL: cfg.Cache.DefaultTTL,
		Log:        slogger,
	})
}

// NOTE: always mounted, so /hooks/ is reserved rather than reaching the console chain.
func buildIngestHandler(cfg *config.Config, log *slog.Logger) http.Handler {
	return ingest.NewHandler(ingest.HandlerParams{
		BasePath: web.Path(cfg.HTTP.BasePath, "/hooks"),
		Log:      log,
	})
}

type webHandlerDeps struct {
	Cfg         *config.Config
	Kernel      *platform.Kernel
	Caps        capabilities.Capabilities
	Log         *slog.Logger
	Reporter    *apperror.Reporter
	HealthOK    func() bool
	Services    *Services
	Required    *atomic.Bool
	OnComplete  func(ctx context.Context)
	SetupToken  string
	APIHandler  http.Handler
	DataHandler http.Handler
	MCP         mcpSurface
	APIResource *tokens.Resource
	Bundle      *i18npkg.Bundle
	DefaultLoc  i18npkg.Locale
}

func buildWebHandler(d webHandlerDeps) (handler http.Handler, routes []string, err error) { //nolint:nonamedreturns // three return values differ in role
	cfg, kernel, slogger, svcs := d.Cfg, d.Kernel, d.Log, d.Services
	deps, err := newWebDeps(cfg, d.Caps, kernel.Sessions, slogger, kernel.StateSecret)
	if err != nil {
		return nil, nil, err
	}
	deps.Orgs = svcs.Orgs
	deps.Projects = svcs.Projects
	deps.I18n = d.Bundle

	authHandler := webhandlers.NewAuthHandler(deps, svcs.Auth, svcs.Users, svcs.Orgs, svcs.Projects, kernel.AltAuth, d.Required)
	onboardingHandler := webhandlers.NewOnboardingHandler(deps, svcs.Users)
	onboardHandler := webhandlers.NewOnboardHandler(deps, svcs.Users, svcs.Orgs, svcs.Projects, svcs.Onboards, d.Required, d.OnComplete, d.SetupToken)
	homeHandler := webhandlers.NewHomeHandler(deps, svcs.Orgs, svcs.Projects)
	orgHandler := webhandlers.NewOrgHandler(deps, svcs.Orgs)
	projectHandler := webhandlers.NewProjectHandler(deps, svcs.Projects)
	todoHandler := webhandlers.NewTodoHandler(deps, svcs.Projects, svcs.Todos)
	apiKeyHandler := webhandlers.NewAPIKeyHandler(deps, svcs.Projects, svcs.APIKeys).WithResources(webhandlers.SheetResources(svcs.Sheets))
	webhookHandler := webhandlers.NewWebhookHandler(deps, svcs.Projects, svcs.Webhooks)
	inviteHandler := webhandlers.NewInviteHandler(deps, svcs.Orgs, svcs.Invites)
	localeHandler := webhandlers.NewLocaleHandler(deps, svcs.Users)
	welcomeHandler := webhandlers.NewWelcomeHandler(deps, svcs.Users)
	signupHandler := webhandlers.NewSignupHandler(deps, svcs.Users, svcs.Orgs, svcs.Projects)
	legalHandler := webhandlers.NewLegalHandler(deps)

	errTmpl := webmw.LogError{Log: slogger}

	handler, routes = web.NewServerWithRoutes(web.ServerOpts{
		BasePath: cfg.HTTP.BasePath,
		HealthOK: d.HealthOK,
		AppHandlers: publishedConsoleHandlers(append([]web.Register{
			authHandler,
			onboardingHandler,
			onboardHandler,
			homeHandler,
			orgHandler,
			projectHandler,
			todoHandler,
			apiKeyHandler,
			webhookHandler,
			inviteHandler,
			localeHandler,
			welcomeHandler,
			signupHandler,
			legalHandler,
		}, opensheetConsoleHandlers(deps, svcs)...)),
		APIHandler:       d.APIHandler,
		DataHandler:      d.DataHandler,
		IngestHandler:    buildIngestHandler(cfg, slogger),
		MCPHandler:       d.MCP.Handler,
		ResourceMetadata: resourceMetadata(cfg, d.MCP, d.APIResource),
		RobotsCfg:        &struct{ RobotsTxt string }{RobotsTxt: cfg.HTTP.RobotsTxt},
		Chains:           surfaceChains(cfg, kernel, slogger, d.Reporter, errTmpl, d.Bundle, d.DefaultLoc, d.Required, deps.Key(web.PurposeSession)),
	})
	return handler, routes, nil
}

// NOTE: the control plane and MCP are separate protected resources, each with its own RFC 9728 document on the Probes chain.
func resourceMetadata(cfg *config.Config, mcp mcpSurface, resource *tokens.Resource) map[string]http.Handler {
	out := map[string]http.Handler{}
	if mcp.Metadata != nil {
		out[mcp.MetadataPath] = mcp.Metadata
	}
	if resource == nil {
		return out
	}
	out[resource.MetadataPath] = resource.CLIMetadataHandler(cfg.Tokens.Issuer, apiScopes(), cfg.OIDC.CLIClientID)
	return out
}

// NOTE: nil when the API is off, no token issuer is set or tokens.audience cannot be an RFC 9728 resource identifier.
func apiResource(cfg *config.Config, log *slog.Logger) *tokens.Resource {
	if !cfg.API.Enabled || cfg.Tokens.Issuer == "" {
		return nil
	}
	resource, err := tokens.NewResource(cfg.Tokens.Audience)
	if err != nil {
		log.Warn("boot: tokens.audience is not an http(s) resource identifier, so the API serves no RFC 9728 document",
			slog.String("audience", cfg.Tokens.Audience), slog.String("err", err.Error()))
		return nil
	}
	return &resource
}

func apiScopes() []string {
	set := map[string]struct{}{}
	for _, scope := range controlplane.ScopeTable() {
		if authn.Mintable(scope) {
			set[scope] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

func surfaceChains(
	cfg *config.Config,
	kernel *platform.Kernel,
	slogger *slog.Logger,
	reporter *apperror.Reporter,
	errTmpl webmw.ErrorTemplate,
	bundle *i18npkg.Bundle,
	defaultLoc i18npkg.Locale,
	required *atomic.Bool,
	secret []byte,
) web.SurfaceChains {
	edge := []web.Middleware{
		webmw.RequestID,
		webmw.RequestLog(slogger),
		webmw.OTel,
	}
	return web.SurfaceChains{
		Probes: slices.Concat(edge, []web.Middleware{
			webmw.Recover(reporter.Unexpected, nil),
		}),
		Console: slices.Concat(edge, []web.Middleware{
			webmw.CSP(cspOptions(cfg)),
			webmw.Recover(reporter.Unexpected, errTmpl),
			webmw.Session(webmw.SessionConfig{
				Store:  kernel.Sessions,
				Secret: secret,
			}),
			webmw.Tenant,
			i18npkg.Middleware(i18npkg.MiddlewareOpts{
				Bundle:     bundle,
				Default:    defaultLoc,
				UserLookup: sessionLocaleLookup,
			}),
			webhandlers.OnboardingGate(cfg.HTTP.BasePath, required),
			webhandlers.WelcomeGate(cfg.HTTP.BasePath, cfg.Compliance.RequireAcceptance),
		}),
		Control: edge,
		Data: slices.Concat(edge, []web.Middleware{
			webmw.RecoverJSON(reporter.Unexpected),
		}),
		Ingest: slices.Concat(edge, []web.Middleware{
			webmw.RecoverJSON(reporter.Unexpected),
		}),
		MCP: slices.Concat(edge, []web.Middleware{
			webmw.RecoverJSON(reporter.Unexpected),
		}),
	}
}

func healthOnlyHandler(cfg *config.Config, healthOK func() bool) http.Handler {
	return web.NewServer(web.ServerOpts{
		BasePath:  cfg.HTTP.BasePath,
		HealthOK:  healthOK,
		RobotsCfg: &struct{ RobotsTxt string }{RobotsTxt: cfg.HTTP.RobotsTxt},
	})
}

func buildI18nBundle(cfg *config.Config) (*i18npkg.Bundle, i18npkg.Locale, error) {
	tag := cfg.I18n.DefaultLocale
	if tag == "" {
		tag = string(i18npkg.EnUS)
	}
	tmp := i18npkg.NewEmbeddedBundle(i18npkg.EnUS)
	loc, err := tmp.Parse(tag)
	if err != nil {
		return nil, "", fmt.Errorf("i18n: default locale %q not among embedded locales", tag)
	}
	return i18npkg.NewEmbeddedBundle(loc), loc, nil
}

func sessionLocaleLookup(ctx context.Context) string {
	return session.PrincipalFrom(ctx).Locale
}

func newWebDeps(cfg *config.Config, caps capabilities.Capabilities, sessions session.Store, slogger *slog.Logger, stateSecret []byte) (webhandlers.Deps, error) {
	return webhandlers.NewDeps(webhandlers.Deps{
		Cfg:      cfg,
		Caps:     caps,
		Sessions: sessions,
		Logger:   stdlog.New(logSlogWriter{log: slogger}, "", 0),
	}, stateSecret)
}

type logSlogWriter struct{ log *slog.Logger }

func (w logSlogWriter) Write(p []byte) (int, error) {
	if w.log != nil {
		w.log.Info(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func cspOptions(cfg *config.Config) webmw.CSPOptions {
	opts := webmw.CSPOptions{
		Enabled:    cfg.HTTP.CSP.Enabled,
		ReportOnly: cfg.HTTP.CSP.ReportOnly,
		ReportURI:  cfg.HTTP.CSP.ReportURI,
	}
	if origin := web.Origin(cfg.OIDC.Issuer); origin != "" && cfg.OIDC.EndSessionOnLogout {
		opts.ExtraFormAction = []string{origin}
	}
	opts.ExtraFormAction = append(opts.ExtraFormAction, opensheetFormAction(cfg)...)
	return opts
}
