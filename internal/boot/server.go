// Package boot is the composition root: it wires the platform Kernel plus every domain service under one worker Supervisor.
package boot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"altalune.id/template/authl"
	"altalune.id/template/internal/api"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/notify"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/platform/tokens"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/todo"
	"altalune.id/template/internal/user"
	"altalune.id/template/logger"
	"altalune.id/template/mailer"
	"altalune.id/template/nanoid"
	"altalune.id/template/scheduler"
	"altalune.id/template/telemetry"
	"altalune.id/template/worker"
)

const setupTokenLen = 32

// Server is the fully-wired dependency graph produced by BootServer.
type Server struct {
	Cfg      *config.Config
	Caps     capabilities.Capabilities
	Platform *platform.Kernel

	Auth     *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Todos    *todo.Service
	Invites  *invite.Service
	Onboards *onboard.Service

	Onboard *user.OnboardWorkflow

	Onboarded bool

	// SetupToken gates /onboard while onboarding is still required; empty once onboarded.
	SetupToken string

	Web        http.Handler
	API        *api.Server
	Scheduler  *scheduler.Runner
	Health     *db.HealthMonitor
	Supervisor *worker.Supervisor

	shutdownOTel func(context.Context) error
}

// BootServer builds every dependency and returns a wired [Server].
func BootServer(ctx context.Context, cfg *config.Config, opts ...Option) (*Server, error) {
	o := newOptions()
	for _, opt := range opts {
		opt(o)
	}

	log := o.logger
	if log == nil {
		log = logger.New(cfg.Log)
	}

	tp, mp, shutdownOTel, err := telemetry.Setup(ctx, cfg.Telemetry, log)
	if err != nil {
		return nil, fmt.Errorf("boot: telemetry: %w", err)
	}

	mail, err := mailer.New(mailerConfig(cfg.Mail))
	if err != nil {
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf("boot: mailer: %w", err)
	}

	sinks := notify.Build(cfg.Observability.Reporter, mail, log)
	reporter := apperror.NewReporter(log, cfg.Mode.IsProduction(),
		apperror.WithContextMeta(tenant.ContextMeta),
		apperror.WithSinks(sinks...),
	)

	pool, pgConn, err := openDBAndMigrate(ctx, cfg, log)
	if err != nil {
		_ = shutdownOTel(context.Background())
		return nil, err
	}

	verifier, err := tokens.NewVerifier(ctx, cfg.Tokens)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf("boot: tokens: %w", err)
	}

	altAuth, err := buildAltAuth(ctx, cfg, log)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf("boot: authl: %w", err)
	}

	sessions := session.NewMemoryStore()
	caps := capabilities.From(cfg)

	kernel := &platform.Kernel{
		Pool:     pool,
		PgConn:   pgConn,
		Log:      log,
		Reporter: reporter,
		Sessions: sessions,
		Verifier: verifier,
		Mail:     mail,
		AltAuth:  altAuth,
		Tracer:   tp.Tracer("altalune.id/template"),
		Meter:    mp.Meter("altalune.id/template"),
		Notify:   sinks,
		Nano:     nanoid.New,
		Caps:     caps,
	}
	for _, s := range sinks {
		if c, ok := s.(io.Closer); ok {
			kernel.AddCloser(c)
		}
	}
	kernel.AddCloser(pool)

	svcs, err := buildServices(cfg, kernel, caps)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, err
	}

	onboarded, err := bootstrap(ctx, cfg, svcs.Users, svcs.Onboards, log)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf("boot: bootstrap: %w", err)
	}
	caps.OnboardingRequired = !onboarded
	if !caps.LocalIdentity {
		has, hErr := svcs.Users.HasLocalUsers(ctx)
		if hErr != nil {
			log.Warn("boot: HasLocalUsers probe failed", slog.String("err", hErr.Error()))
		} else if has {
			caps.LocalIdentity = true
		}
	}
	kernel.Caps = caps

	health := db.NewHealthMonitor(pool, cfg.DB.Health, log, kernel.Meter)
	if pErr := health.Probe(ctx); pErr != nil {
		log.Warn("boot: initial db health probe failed", slog.String("err", pErr.Error()))
	}

	sup := worker.New(log)
	sup.Register(health)
	if cfg.Telemetry.Metrics.Prometheus.Enabled {
		sup.Register(telemetry.PrometheusWorker(cfg.Telemetry.Metrics.Prometheus, log))
	}

	var runner *scheduler.Runner
	switch {
	case !o.scheduler:
		log.Info("boot: scheduler disabled by WithScheduler(false)")
	case !cfg.Scheduler.Enabled:
		log.Info("boot: scheduler disabled by scheduler.enabled=false")
	default:
		r, sErr := buildScheduler(cfg, kernel, svcs, log)
		if sErr != nil {
			_ = pool.Close()
			_ = shutdownOTel(context.Background())
			return nil, sErr
		}
		runner = r
		sup.Register(runner)
	}

	if o.schedulerOnly && runner == nil {
		disabledBy := "scheduler.enabled=false"
		if !o.scheduler {
			disabledBy = "WithScheduler(false)"
		}
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf(
			"boot: --scheduler-only requires the scheduler, but the scheduler is disabled by %s", disabledBy)
	}

	apiSrv, apiHandler := buildAPIHandler(cfg, kernel, svcs)

	bundle, defaultLoc, err := buildI18nBundle(cfg)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, err
	}

	healthOK := health.Ready

	required := &atomic.Bool{}
	required.Store(!onboarded)

	var setup string
	if required.Load() {
		t, tErr := setupToken(cfg)
		if tErr != nil {
			_ = pool.Close()
			_ = shutdownOTel(context.Background())
			return nil, fmt.Errorf("boot: setup token: %w", tErr)
		}
		setup = t
		logSetupToken(cfg, log, setup)
	}

	webHandler := buildWebHandler(cfg, kernel, caps, log, reporter, healthOK,
		svcs.Auth, svcs.Users, svcs.Orgs, svcs.Projects, svcs.Todos, svcs.Invites, svcs.Onboards, required, setup, apiHandler, bundle, defaultLoc)

	httpHandler := webHandler
	if o.schedulerOnly {
		httpHandler = healthOnlyHandler(cfg, healthOK)
	}
	sup.Register(worker.HTTP("http", cfg.HTTP.Addr, httpHandler, log))

	return &Server{
		Cfg:          cfg,
		Caps:         caps,
		Platform:     kernel,
		Auth:         svcs.Auth,
		Users:        svcs.Users,
		Orgs:         svcs.Orgs,
		Projects:     svcs.Projects,
		Todos:        svcs.Todos,
		Invites:      svcs.Invites,
		Onboards:     svcs.Onboards,
		Onboarded:    onboarded,
		SetupToken:   setup,
		Onboard:      svcs.Onboard,
		Web:          webHandler,
		API:          apiSrv,
		Scheduler:    runner,
		Health:       health,
		Supervisor:   sup,
		shutdownOTel: shutdownOTel,
	}, nil
}

// Run starts every registered worker and blocks until ctx is canceled or a worker fails.
func (s *Server) Run(ctx context.Context) error {
	return s.Supervisor.Run(ctx)
}

// Close shuts OTel down and then releases every platform resource.
func (s *Server) Close() error {
	var errs []error
	if s.shutdownOTel != nil {
		if err := s.shutdownOTel(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	if s.Platform != nil {
		if err := s.Platform.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func setupToken(cfg *config.Config) (string, error) {
	if t := strings.TrimSpace(cfg.Onboard.SetupToken); t != "" {
		return t, nil
	}
	return nanoid.New(setupTokenLen)
}

func logSetupToken(cfg *config.Config, log *slog.Logger, token string) {
	url := onboardURL(cfg)
	if strings.TrimSpace(cfg.Onboard.SetupToken) != "" {
		log.Info("boot: setup required — /onboard is gated by the configured onboard.setupToken",
			slog.String("url", url))
		return
	}
	// SECURITY: the token rides in the url value because logger.Redact masks any attr key matching /token/,
	// which would otherwise leave a fresh deployment unable to reach its own setup page.
	log.Info("boot: setup required — open this one-time onboarding URL",
		slog.String("url", url+"?token="+token),
	)
}

func onboardURL(cfg *config.Config) string {
	return strings.TrimRight(cfg.HTTP.BaseURL, "/") + cfg.HTTP.BasePath + "/onboard"
}

// NOTE: path must match "GET /oauth/callback" in internal/web/handlers/auth.go.
func oidcRedirectURL(cfg *config.Config) string {
	if cfg.HTTP.BaseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s%s/oauth/callback",
		strings.TrimRight(cfg.HTTP.BaseURL, "/"), cfg.HTTP.BasePath)
}

func buildAltAuth(ctx context.Context, cfg *config.Config, log *slog.Logger) (*authl.Client, error) {
	if cfg.OIDC.Issuer == "" {
		return nil, nil
	}
	secret, err := resolveStateSecret(cfg, log)
	if err != nil {
		return nil, err
	}
	redirect := oidcRedirectURL(cfg)
	return authl.NewClient(ctx, authl.Config{
		Issuer:           cfg.OIDC.Issuer,
		ClientID:         cfg.OIDC.ClientID,
		ClientSecret:     cfg.OIDC.ClientSecret,
		RedirectURL:      redirect,
		Scopes:           cfg.OIDC.Scopes,
		Resource:         cfg.OIDC.Resource,
		RememberLastUser: true,
		LastUserCookie:   "altempl_last_user",
		StateCookie:      "altempl_oidc_state",
		StateSecret:      secret,
		CookieSecure:     cfg.HTTP.CookieSecure,
	})
}

func resolveStateSecret(cfg *config.Config, log *slog.Logger) ([]byte, error) {
	raw := cfg.HTTP.StateSecret
	if raw != "" {
		buf, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			if b2, err2 := base64.StdEncoding.DecodeString(raw); err2 == nil {
				buf = b2
			} else {
				return nil, fmt.Errorf("config: http.stateSecret must be base64url — %w", err)
			}
		}
		if len(buf) < 32 {
			return nil, errors.New("config: http.stateSecret must decode to >= 32 bytes")
		}
		return buf, nil
	}
	ephemeral, err := authl.GenerateStateSecret()
	if err != nil {
		return nil, fmt.Errorf("mint ephemeral state secret: %w", err)
	}
	log.Warn("http.stateSecret is empty — using an ephemeral secret; set ALT_HTTP_STATE_SECRET to persist")
	return ephemeral, nil
}

func mailerConfig(m config.MailConfig) mailer.Config {
	return mailer.Config{
		Driver: m.Driver,
		From:   m.From,
		SMTP: mailer.SMTPConfig{
			Host: m.SMTP.Host,
			Port: m.SMTP.Port,
			User: m.SMTP.User,
			Pass: m.SMTP.Pass,
			TLS:  m.SMTP.TLS,
		},
		Resend: mailer.ResendConfig{
			APIKey:      m.Resend.APIKey,
			Endpoint:    m.Resend.Endpoint,
			MaxAttempts: m.Resend.MaxAttempts,
		},
	}
}
