// Package boot is the composition root: it wires the platform Kernel plus every domain service under one worker Supervisor.
package boot

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/altalune-id/oidcrp"

	"altalune.id/opensheet/httpclient"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/notify"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/webhook"
	"altalune.id/opensheet/logger"
	"altalune.id/opensheet/mailer"
	rootmcp "altalune.id/opensheet/mcp"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/scheduler"
	"altalune.id/opensheet/telemetry"
	"altalune.id/opensheet/worker"
)

const setupTokenLen = 32

const oidcTimeout = 10 * time.Second

const (
	webhookTimeout       = 10 * time.Second
	webhookResponseLimit = 64 << 10
)

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
	APIKeys  *apikey.Service
	Webhooks *webhook.Service

	opensheetServices

	Onboard *user.OnboardWorkflow

	Onboarded bool

	// SetupToken gates /onboard while onboarding is still required; empty once onboarded.
	SetupToken string

	Web http.Handler
	// Routes are the app-route patterns the web handlers registered, taken from the mux.
	Routes    []string
	API       *controlplane.Server
	MCP       *rootmcp.Server
	Scheduler *scheduler.Runner
	// Consumer runs this instance's queue jobs; nil when it does not run here.
	Consumer *queue.Consumer
	// Listener applies broadcasts to this instance; nil when the queue is disabled.
	Listener   *queue.Listen
	Health     *db.HealthMonitor
	Supervisor *worker.Supervisor

	// CompleteOnboarding clears the setup gate here and on every other running instance.
	CompleteOnboarding func(ctx context.Context)

	httpHandler  http.Handler
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

	oidcHTTP := o.oidcHTTP
	if oidcHTTP == nil {
		oidcHTTP = oidcHTTPClient()
	}
	tokenKeys := tokens.NewKeySource(cfg.Tokens, oidcHTTP)
	verifier, err := tokens.NewVerifier(cfg.Tokens, tokenKeys)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf("boot: tokens: %w", err)
	}

	stateSecret, err := resolveStateSecret(cfg, log)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, err
	}

	altAuth := buildOIDCClient(cfg, oidcHTTP)

	sl, err := buildSealer(cfg, log)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, err
	}

	sessions := session.NewStore(cfg.DB, pool, sl, reporter.Unexpected)
	caps := capabilities.From(cfg)

	kernel := &platform.Kernel{
		Pool:            pool,
		PgConn:          pgConn,
		Log:             log,
		Reporter:        reporter,
		Sessions:        sessions,
		Sealer:          sl,
		Verifier:        verifier,
		Mail:            mail,
		AltAuth:         altAuth,
		OIDCStateSecret: stateSecret,
		Tracer:          tp.Tracer("altalune.id/opensheet"),
		Meter:           mp.Meter("altalune.id/opensheet"),
		Notify:          sinks,
		Nano:            nanoid.New,
		Caps:            caps,
		Outbox:          outbox.NewStore(cfg.DB, pool, pgConn),
	}
	for _, s := range sinks {
		if c, ok := s.(io.Closer); ok {
			kernel.AddCloser(c)
		}
	}
	kernel.AddCloser(pool)

	q, err := openQueue(ctx, cfg, kernel, reporter, log)
	if err != nil {
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
		return nil, fmt.Errorf("boot: queue: %w", err)
	}
	kernel.Queue = q
	kernel.AddCloser(q)
	abort := func() {
		_ = q.Close()
		_ = pool.Close()
		_ = shutdownOTel(context.Background())
	}

	startSeq, err := q.BroadcastStartSeq(ctx)
	if err != nil {
		abort()
		return nil, fmt.Errorf("boot: queue: %w", err)
	}

	required := &atomic.Bool{}

	svcs, err := buildServices(cfg, kernel, caps)
	if err != nil {
		abort()
		return nil, err
	}
	svcs.opensheetServices, err = buildOpensheetServices(cfg, kernel, caps)
	if err != nil {
		abort()
		return nil, err
	}

	gate := &onboardingGate{required: required, queue: q, unexpected: reporter.Unexpected}
	hs, ls, err := declareQueue(q, svcs, gate, log)
	if err != nil {
		abort()
		return nil, err
	}

	onboarded, err := bootstrap(ctx, cfg, svcs.Users, svcs.Onboards, log)
	if err != nil {
		abort()
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
	sup.Register(svcs.APIKeyUsage)
	sup.Register(outbox.NewWorker(kernel.Outbox, dispatchDeliverer(o, kernel, svcs, log), orgEnumerator(cfg, kernel, log), log, o.dispatch))
	if cfg.Telemetry.Metrics.Prometheus.Enabled {
		sup.Register(telemetry.PrometheusWorker(cfg.Telemetry.Metrics.Prometheus, log))
	}

	var runner *scheduler.Runner
	switch off := schedulerOff(o, cfg); off {
	case "":
		r, sErr := buildScheduler(cfg, kernel, svcs, log)
		if sErr != nil {
			abort()
			return nil, sErr
		}
		runner = r
		sup.Register(runner)
	default:
		log.Info("boot: scheduler disabled by " + off)
	}

	if o.schedulerOnly && runner == nil {
		abort()
		return nil, fmt.Errorf(
			"boot: --scheduler-only requires the scheduler, but the scheduler is disabled by %s", schedulerOff(o, cfg))
	}

	var consumer *queue.Consumer
	switch off := consumerOff(o, cfg); off {
	case "":
		c, cErr := queue.NewConsumer(ctx, q, hs)
		if cErr != nil {
			abort()
			return nil, fmt.Errorf("boot: queue consumer: %w", cErr)
		}
		consumer = c
		sup.Register(consumer)
	default:
		log.Info("boot: consumer disabled by " + off)
	}

	if o.consumerOnly && consumer == nil {
		abort()
		return nil, fmt.Errorf(
			"boot: --consumer-only requires the queue, but the queue is disabled by %s", consumerOff(o, cfg))
	}

	var listener *queue.Listen
	if cfg.Queue.Enabled {
		l, lErr := queue.NewListener(q, ls, startSeq)
		if lErr != nil {
			abort()
			return nil, fmt.Errorf("boot: queue listener: %w", lErr)
		}
		listener = l
		sup.Register(listener)
	}

	apiSrv, apiHandler := buildAPIHandler(cfg, kernel, svcs)
	dataHandler := buildDataHandler(cfg, caps, log, svcs)

	mcpSurf, err := buildMCPSurface(cfg, log, svcs, apiSrv, tokenKeys)
	if err != nil {
		abort()
		return nil, err
	}

	bundle, defaultLoc, err := buildI18nBundle(cfg)
	if err != nil {
		abort()
		return nil, err
	}

	healthOK := health.Ready

	required.Store(!onboarded)

	var setup string
	if required.Load() {
		t, tErr := setupToken(cfg)
		if tErr != nil {
			abort()
			return nil, fmt.Errorf("boot: setup token: %w", tErr)
		}
		setup = t
		logSetupToken(cfg, log, setup)
	}

	webHandler, webRoutes, err := buildWebHandler(webHandlerDeps{
		Cfg:         cfg,
		Kernel:      kernel,
		Caps:        caps,
		Log:         log,
		Reporter:    reporter,
		HealthOK:    healthOK,
		Services:    svcs,
		Required:    required,
		OnComplete:  gate.Complete,
		SetupToken:  setup,
		APIHandler:  apiHandler,
		DataHandler: dataHandler,
		MCP:         mcpSurf,
		Bundle:      bundle,
		DefaultLoc:  defaultLoc,
	})
	if err != nil {
		abort()
		return nil, err
	}

	httpHandler := webHandler
	if o.schedulerOnly || o.consumerOnly {
		httpHandler = healthOnlyHandler(cfg, healthOK)
	}
	sup.Register(worker.HTTP("http", cfg.HTTP.Addr, httpHandler, log))

	return &Server{
		Cfg:                cfg,
		Caps:               caps,
		Platform:           kernel,
		Auth:               svcs.Auth,
		Users:              svcs.Users,
		Orgs:               svcs.Orgs,
		Projects:           svcs.Projects,
		Todos:              svcs.Todos,
		Invites:            svcs.Invites,
		Onboards:           svcs.Onboards,
		APIKeys:            svcs.APIKeys,
		Webhooks:           svcs.Webhooks,
		opensheetServices:  svcs.opensheetServices,
		Onboarded:          onboarded,
		Routes:             webRoutes,
		SetupToken:         setup,
		Onboard:            svcs.Onboard,
		Web:                webHandler,
		API:                apiSrv,
		MCP:                mcpSurf.Server,
		Scheduler:          runner,
		Consumer:           consumer,
		Listener:           listener,
		CompleteOnboarding: gate.Complete,
		httpHandler:        httpHandler,
		Health:             health,
		Supervisor:         sup,
		shutdownOTel:       shutdownOTel,
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

func dispatchDeliverer(o *options, k *platform.Kernel, svcs *Services, log *slog.Logger) outbox.Deliverer {
	if o.deliverer != nil {
		return o.deliverer
	}
	return webhook.NewDeliverer(svcs.WebhookStore, k.Sealer, webhookHTTPClient(), log)
}

// SECURITY: otel stays off — otelhttp records url.full including the query string, and webhook.Deliver's own span is the only trace a delivery should leave.
func webhookHTTPClient() *http.Client {
	return httpclient.New(
		httpclient.WithTimeout(webhookTimeout),
		httpclient.WithResponseBodyLimit(webhookResponseLimit),
		httpclient.WithOtel(false),
	)
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
	// SECURITY: the token rides in the url value because logger.Redact masks any attr key matching /token/.
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

// NOTE: nil without oidc.issuer; private hosts are allowed because the issuer is operator config, like a webhook URL is.
func buildOIDCClient(cfg *config.Config, client *http.Client) *oidcrp.Client {
	if cfg.OIDC.Issuer == "" {
		return nil
	}
	return oidcrp.New(oidcrp.Config{
		Issuer:       cfg.OIDC.Issuer,
		ClientID:     cfg.OIDC.ClientID,
		ClientSecret: cfg.OIDC.ClientSecret,
		RedirectURI:  oidcRedirectURL(cfg),
		Scopes:       cfg.OIDC.Scopes,
		HTTPClient:   client,
	})
}

func resolveStateSecret(cfg *config.Config, log *slog.Logger) ([]byte, error) {
	if cfg.HTTP.StateSecret != "" {
		return config.ParseStateSecret(cfg.HTTP.StateSecret)
	}
	ephemeral := make([]byte, config.StateSecretMinLen)
	if _, err := rand.Read(ephemeral); err != nil {
		return nil, fmt.Errorf("mint ephemeral state secret: %w", err)
	}
	log.Warn("http.stateSecret is empty — using an ephemeral secret; set OPENSHEET_HTTP_STATE_SECRET to persist")
	return ephemeral, nil
}

// SECURITY: oidcrp and oauthrs need a client that never follows a redirect off the issuer.
func oidcHTTPClient() *http.Client {
	c := httpclient.New(httpclient.WithAllowPrivateHosts(true), httpclient.WithTimeout(oidcTimeout))
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
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

func openQueue(ctx context.Context, cfg *config.Config, k *platform.Kernel, reporter *apperror.Reporter, log *slog.Logger) (*queue.Client, error) {
	if !cfg.Queue.Enabled {
		return queue.Disabled(log), nil
	}
	return queue.Connect(ctx, queue.Options{
		URL:            cfg.Queue.URL,
		Token:          cfg.Queue.Token,
		User:           cfg.Queue.User,
		Password:       cfg.Queue.Password,
		ConnectTimeout: cfg.Queue.ConnectTimeout,
		Log:            log,
		Tracer:         k.Tracer,
		Meter:          k.Meter,
		Unexpected:     reporter.Unexpected,
	})
}

func declareQueue(q *queue.Client, svcs *Services, gate *onboardingGate, log *slog.Logger) ([]queue.Handler, []queue.Listener, error) {
	providers := consumerProviders(svcs, log)
	if err := assertConsumerWiring(providers); err != nil {
		return nil, nil, err
	}
	hs := handlersOf(publishedConsumers(providers))
	ls := listenersOf(slices.Concat(listenerProviders(gate), opensheetListenerProviders(svcs)))
	if err := q.Declare(jobsOf(hs), broadcastsOf(ls)); err != nil {
		return nil, nil, fmt.Errorf("boot: queue declare: %w", err)
	}
	return hs, ls, nil
}

func schedulerOff(o *options, cfg *config.Config) string {
	switch {
	case !o.scheduler:
		return "WithScheduler(false)"
	case o.consumerOnly:
		return "--consumer-only"
	case !cfg.Scheduler.Enabled:
		return "scheduler.enabled=false"
	}
	return ""
}

func consumerOff(o *options, cfg *config.Config) string {
	switch {
	case !o.consumer:
		return "WithConsumer(false)"
	case o.schedulerOnly:
		return "--scheduler-only"
	case !cfg.Queue.Enabled:
		return "queue.enabled=false"
	}
	return ""
}
