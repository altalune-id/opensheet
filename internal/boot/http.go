package boot

import (
	"context"
	"fmt"
	stdlog "log"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"altalune.id/opensheet/internal/api"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/data"
	i18npkg "altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web"
	webhandlers "altalune.id/opensheet/internal/web/handlers"
	webmw "altalune.id/opensheet/internal/web/middleware"
)

func buildAPIHandler(cfg *config.Config, k *platform.Kernel, s *Services) (*api.Server, http.Handler) {
	srv := api.New(api.Deps{
		Cfg: cfg, Kernel: k, Authn: s.Authn,
		Auths: s.Auth, Users: s.Users, Orgs: s.Orgs, Projects: s.Projects,
		Todos: s.Todos, Invites: s.Invites, Credentials: s.Credentials,
		Spreadsheets: s.Spreadsheets, Sheets: s.Sheets, APIKeys: s.APIKeys,
		CredentialConnect: s.Connect,
		TodoStore:         s.TodoStore,
	})
	if !cfg.API.Enabled {
		return srv, nil
	}
	h := srv.Handler(cfg.HTTP.BasePath)
	return srv, h
}

// NOTE: the data plane is built regardless of api.enabled — that flag gates only the RPC surface.
func buildDataHandler(cfg *config.Config, caps capabilities.Capabilities, log *slog.Logger, s *Services) http.Handler {
	return data.NewHandler(data.HandlerParams{
		BasePath:   cfg.HTTP.BasePath,
		Orgs:       orgsForData{svc: s.Orgs},
		Projects:   projectsForData{svc: s.Projects},
		Sheets:     sheetsForData{svc: s.Sheets},
		Reader:     s.Read,
		Purger:     s.Sheets,
		Writer:     s.Write,
		Tabs:       s.Spreadsheets,
		Authz:      s.KeyAuthn,
		Caps:       capsForSheet{caps: caps},
		DefaultTTL: cfg.Cache.DefaultTTL,
		Log:        log,
	})
}

func buildWebHandler(
	cfg *config.Config,
	kernel *platform.Kernel,
	caps capabilities.Capabilities,
	slogger *slog.Logger,
	reporter *apperror.Reporter,
	healthOK func() bool,
	auths *auth.Service,
	users *user.Service,
	orgs *org.Service,
	projects *project.Service,
	todos *todo.Service,
	invites *invite.Service,
	svcs *Services,
	onboards *onboard.Service,
	required *atomic.Bool,
	setupToken string,
	apiHandler http.Handler,
	dataHandler http.Handler,
	bundle *i18npkg.Bundle,
	defaultLoc i18npkg.Locale,
	stateSecret []byte,
) (http.Handler, error) {
	deps, err := newWebDeps(cfg, caps, kernel.Sessions, slogger, stateSecret)
	if err != nil {
		return nil, fmt.Errorf("boot: web deps: %w", err)
	}
	deps.Orgs = orgs
	deps.Projects = projects
	deps.I18n = bundle

	authHandler := webhandlers.NewAuthHandler(deps, auths, users, orgs, projects, kernel.AltAuth, required)
	onboardingHandler := webhandlers.NewOnboardingHandler(deps, users)
	onboardHandler := webhandlers.NewOnboardHandler(deps, users, orgs, projects, onboards, required, setupToken)
	homeHandler := webhandlers.NewHomeHandler(deps, orgs, projects)
	orgHandler := webhandlers.NewOrgHandler(deps, orgs)
	projectHandler := webhandlers.NewProjectHandler(deps, projects)
	todoHandler := webhandlers.NewTodoHandler(deps, projects, todos)
	credentialHandler := webhandlers.NewCredentialHandler(deps, projects, svcs.Credentials, svcs.Connect)
	spreadsheetHandler := webhandlers.NewSpreadsheetHandler(deps, projects, svcs.Spreadsheets, svcs.Credentials)
	sheetHandler := webhandlers.NewSheetHandler(deps, projects, svcs.Sheets, svcs.Spreadsheets, svcs.Read)
	apiKeyHandler := webhandlers.NewAPIKeyHandler(deps, projects, svcs.APIKeys, svcs.Sheets)
	googleHandler := webhandlers.NewGoogleConnectHandler(deps, projects, svcs.Credentials, svcs.Connect)
	inviteHandler := webhandlers.NewInviteHandler(deps, orgs, invites)
	localeHandler := webhandlers.NewLocaleHandler(deps, users)
	welcomeHandler := webhandlers.NewWelcomeHandler(deps, users)
	signupHandler := webhandlers.NewSignupHandler(deps, users, orgs, projects)
	legalHandler := webhandlers.NewLegalHandler(deps)

	errTmpl := webmw.LogError{Log: slogger}

	return web.NewServer(web.ServerOpts{
		BasePath: cfg.HTTP.BasePath,
		HealthOK: healthOK,
		AppHandlers: []web.Register{
			authHandler, onboardingHandler, onboardHandler, homeHandler, orgHandler, projectHandler, todoHandler,
			credentialHandler, spreadsheetHandler, sheetHandler, apiKeyHandler, googleHandler,
			inviteHandler, localeHandler, welcomeHandler, signupHandler, legalHandler,
		},
		APIHandler:  apiHandler,
		DataHandler: dataHandler,
		RobotsCfg:   &struct{ RobotsTxt string }{RobotsTxt: cfg.HTTP.RobotsTxt},
		Middlewares: []web.Middleware{
			webmw.RequestID,
			webmw.RequestLog(slogger),
			webmw.OTel,
			webmw.Recover(reporter.Unexpected, errTmpl),
			webmw.Session(webmw.SessionConfig{
				Store:  kernel.Sessions,
				Secret: deps.SecretBytes(),
			}),
			webmw.Tenant,
			i18npkg.Middleware(i18npkg.MiddlewareOpts{
				Bundle:     bundle,
				Default:    defaultLoc,
				UserLookup: sessionLocaleLookup,
			}),
			webhandlers.OnboardingGate(cfg.HTTP.BasePath, required),
			webhandlers.WelcomeGate(cfg.HTTP.BasePath, cfg.Compliance.RequireAcceptance),
		},
	}), nil
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

func newWebDeps(cfg *config.Config, caps capabilities.Capabilities, sessions session.Store,
	slogger *slog.Logger, stateSecret []byte) (webhandlers.Deps, error) {
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
