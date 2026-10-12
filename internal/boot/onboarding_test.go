package boot

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/user"
)

type unexpectedRecorder struct {
	calls atomic.Int32
	last  atomic.Pointer[error]
}

func (r *unexpectedRecorder) report(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
	r.calls.Add(1)
	r.last.Store(&err)
	return nil
}

func gateQueue(t *testing.T, url string) *queue.Client {
	t.Helper()
	q, err := queue.Connect(t.Context(), queue.Options{
		URL:            url,
		ConnectTimeout: 5 * time.Second,
		Log:            slog.New(slog.DiscardHandler),
		Unexpected:     func(context.Context, string, error, ...any) *apperror.AppError { return nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	return q
}

func newGate(q *queue.Client, rec *unexpectedRecorder) *onboardingGate {
	required := &atomic.Bool{}
	required.Store(true)
	return &onboardingGate{required: required, queue: q, unexpected: rec.report}
}

func TestOnboardingGate_CompleteClearsAndEmitsOnce(t *testing.T) {
	ns := startNATSServer(t)
	q := gateQueue(t, ns.ClientURL())
	rec := &unexpectedRecorder{}
	g := newGate(q, rec)
	require.NoError(t, q.Declare(nil, broadcastsOf(g.Listeners())))

	g.Complete(t.Context())

	require.False(t, g.required.Load())
	require.Zero(t, rec.calls.Load())
	require.Equal(t, uint64(1), streamMsgs(t, ns.ClientURL(), "BROADCAST"))
}

func TestOnboardingGate_EmitFailureIsReportedAndStillClears(t *testing.T) {
	ns := startNATSServer(t)
	q := gateQueue(t, ns.ClientURL())
	rec := &unexpectedRecorder{}
	g := newGate(q, rec)

	g.Complete(t.Context())

	require.False(t, g.required.Load(), "a failed emit must never keep this instance gated")
	require.Equal(t, int32(1), rec.calls.Load())
	require.True(t, queue.IsUndeclaredBroadcastError(*rec.last.Load()))
}

func TestOnboardingGate_ListenerClears(t *testing.T) {
	g := newGate(queue.Disabled(slog.New(slog.DiscardHandler)), &unexpectedRecorder{})

	ls := g.Listeners()

	require.Len(t, ls, 1)
	require.Equal(t, onboardingCompleted(), ls[0].Broadcast)
	require.NoError(t, ls[0].Handle(t.Context(), queue.Message{}))
	require.False(t, g.required.Load())
}

func onboardingBootCfg(t *testing.T, natsURL string) *config.Config {
	t.Helper()
	cfg := queueBootCfg(t, natsURL)
	cfg.Genesis = config.GenesisConfig{}
	cfg.Scheduler.Enabled = false
	cfg.Onboard.SetupToken = "setup-token-for-test"
	return cfg
}

func redirectsToOnboard(srv *Server) bool {
	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code == http.StatusSeeOther && strings.HasPrefix(rec.Header().Get("Location"), "/onboard")
}

func TestOnboarding_CompletingOnOneInstanceOpensEveryInstance(t *testing.T) {
	ns := startNATSServer(t)
	cfgA := onboardingBootCfg(t, ns.ClientURL())
	cfgB := *cfgA

	a, err := BootServer(t.Context(), cfgA)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	b, err := BootServer(t.Context(), &cfgB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })
	require.False(t, a.Onboarded)
	require.False(t, b.Onboarded)
	require.True(t, redirectsToOnboard(b))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	form := url.Values{
		"token": {cfgA.Onboard.SetupToken}, "email": {"admin@example.com"}, "name": {"Admin"},
		"password": {"secret-password"}, "org_slug": {"acme"}, "org_name": {"Acme"},
		"project_slug": {"default"}, "project_name": {"Default"},
	}
	req := httptest.NewRequest(http.MethodPost, "/onboard/local", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.Web.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	require.Equal(t, "/", rec.Header().Get("Location"))
	require.False(t, redirectsToOnboard(a))

	require.Eventually(t, func() bool { return !redirectsToOnboard(b) }, 5*time.Second, 20*time.Millisecond,
		"the other replica must drop its setup gate once onboarding completes anywhere")
}

func TestOnboarding_CompleteOnboardingIsTheGate(t *testing.T) {
	srv, err := BootServer(t.Context(), onboardingBootCfg(t, ""))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })
	require.True(t, redirectsToOnboard(srv))
	require.NotNil(t, srv.CompleteOnboarding)

	srv.CompleteOnboarding(t.Context())

	require.False(t, redirectsToOnboard(srv))
}

func TestOnboarding_CustomOrgSlugStillResolvesOnLaterLogins(t *testing.T) {
	cfg := onboardingBootCfg(t, "")
	cfg.Tenant.SingletonOrg.Slug = ""
	srv, err := BootServer(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	form := url.Values{
		"token": {cfg.Onboard.SetupToken}, "email": {"admin@example.com"}, "name": {"Admin"},
		"password": {"secret-password"}, "org_slug": {"admin-edited-slug"}, "org_name": {"Acme"},
		"project_slug": {""}, "project_name": {"Main"},
	}
	req := httptest.NewRequest(http.MethodPost, "/onboard/local", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	o, err := srv.Orgs.BySlug(t.Context(), "admin-edited-slug")
	require.NoError(t, err, "the edited slug must be the one stored")

	res, err := srv.Onboard.Onboard(t.Context(), o.OwnerID, "admin@example.com")
	require.NoError(t, err, "a later selfhosted login must find the singleton org by its system flag, not the configured slug")
	require.Equal(t, o.ID, res.OrgID)
	require.NotEqual(t, uuid.Nil, res.ProjectID, "the generated first project must be picked")

	_, err = srv.Onboard.Onboard(t.Context(), uuid.New(), "stranger@example.com")
	require.True(t, user.IsNotInvitedError(err), "an uninvited user must meet the existing org, got %T: %v", err, err)
}
