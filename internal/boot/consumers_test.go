package boot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

type stubConsumerProvider struct{}

func (stubConsumerProvider) ConsumerHandlers() []queue.Handler { return nil }

func TestAssertConsumerWiring(t *testing.T) {
	tests := []struct {
		name      string
		providers []queue.Provider
		wantErr   string
	}{
		{"all wired", []queue.Provider{stubConsumerProvider{}}, ""},
		{"todo slot missing", []queue.Provider{nil}, "todo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := assertConsumerWiring(tt.providers)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestAssertConsumerWiring_LengthMismatchIsAnError(t *testing.T) {
	require.Error(t, assertConsumerWiring(nil))
	require.Error(t, assertConsumerWiring([]queue.Provider{stubConsumerProvider{}, stubConsumerProvider{}}))
}

func TestConsumerDomains_MatchesProviderCount(t *testing.T) {
	require.Len(t, consumerProviders(&Services{}, discardLogger()), len(consumerDomains()),
		"add the new domain to consumerDomains and consumerProviders together")
}

func TestListenersOf_CollectsTheOnboardingBroadcast(t *testing.T) {
	ls := listenersOf(listenerProviders(&onboardingGate{}))
	require.Equal(t, []queue.Broadcast{onboardingCompleted()}, broadcastsOf(ls))
}

func queueBootCfg(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg := schedulerBootCfg(t)
	cfg.Queue = config.QueueConfig{Enabled: url != "", URL: url, ConnectTimeout: 5 * time.Second}
	return cfg
}

func workerNames(srv *Server) []string {
	var names []string
	for _, w := range srv.Supervisor.Workers() {
		names = append(names, w.Name())
	}
	return names
}

func TestBootServer_QueueDisabled(t *testing.T) {
	srv, err := BootServer(t.Context(), queueBootCfg(t, ""))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	require.False(t, srv.Platform.Queue.Enabled())
	require.Nil(t, srv.Consumer)
	require.Nil(t, srv.Listener)
	require.NotContains(t, workerNames(srv), queue.WorkerName)
	require.NotContains(t, workerNames(srv), queue.ListenerName)
}

func TestBootServer_ProcessMatrix(t *testing.T) {
	ns := startNATSServer(t)
	tests := []struct {
		name          string
		opts          []Option
		wantConsumer  bool
		wantScheduler bool
		wantHealthy   bool
	}{
		{"none", nil, true, true, false},
		{"no scheduler", []Option{WithScheduler(false)}, true, false, false},
		{"no consumer", []Option{WithConsumer(false)}, false, true, false},
		{"scheduler only", []Option{WithSchedulerOnly(true)}, false, true, true},
		{"consumer only", []Option{WithConsumerOnly(true)}, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := BootServer(t.Context(), queueBootCfg(t, ns.ClientURL()), tt.opts...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, srv.Close()) })

			names := workerNames(srv)
			require.True(t, srv.Platform.Queue.Enabled())
			require.Equal(t, tt.wantConsumer, srv.Consumer != nil)
			require.Equal(t, tt.wantConsumer, slices.Contains(names, queue.WorkerName), names)
			require.Equal(t, tt.wantScheduler, srv.Scheduler != nil)
			require.NotNil(t, srv.Listener, "the listener runs on every queue-enabled instance")
			require.Contains(t, names, queue.ListenerName)
			require.Contains(t, names, outbox.WorkerName, "the outbox dispatch worker runs on every process shape")
			require.Contains(t, names, "db-health", "db health runs on every process shape")

			rec := httptest.NewRecorder()
			srv.httpHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
			if tt.wantHealthy {
				require.Equal(t, http.StatusNotFound, rec.Code, "a worker-only process serves probes only")
				return
			}
			require.NotEqual(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestBootServer_ConsumerOnlyNeedsTheQueue(t *testing.T) {
	_, err := BootServer(t.Context(), queueBootCfg(t, ""), WithConsumerOnly(true))
	require.EqualError(t, err,
		"boot: --consumer-only requires the queue, but the queue is disabled by queue.enabled=false")
}

func TestBootServer_FailedBootClosesTheQueue(t *testing.T) {
	ns := startNATSServer(t)
	_, err := BootServer(t.Context(), queueBootCfg(t, ns.ClientURL()), WithConsumer(false), WithConsumerOnly(true))
	require.EqualError(t, err,
		"boot: --consumer-only requires the queue, but the queue is disabled by WithConsumer(false)")
	require.Eventually(t, func() bool { return ns.NumClients() == 0 }, 5*time.Second, 20*time.Millisecond,
		"a failed boot must not leak a reconnecting NATS connection")
}

func TestBootServer_ProducerOnlyStillDeclaresJobs(t *testing.T) {
	if !mountTodo {
		t.Skip("opensheet: todo is not mounted, so no queue job is declared; see internal/boot/surfaces_opensheet.go")
	}
	ns := startNATSServer(t)
	srv, err := BootServer(t.Context(), queueBootCfg(t, ns.ClientURL()), WithConsumer(false))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })
	require.Nil(t, srv.Consumer)

	ctx := t.Context()
	owner, err := srv.Users.Create(ctx, user.CreateRequest{Email: "q-owner@example.com", Name: "Q", Source: user.SourceGenesis})
	require.NoError(t, err)
	o, err := srv.Orgs.BootstrapSingleton(ctx, "q-org", "Q Org", owner.ID)
	require.NoError(t, err)
	orgCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	p, err := srv.Projects.Create(orgCtx, o.ID, "q-project", "Q Project")
	require.NoError(t, err)
	projCtx := tenant.WithProject(orgCtx, p.ID)

	td, err := srv.Todos.Create(projCtx, "ship it")
	require.NoError(t, err)
	_, err = srv.Todos.Toggle(projCtx, td.ID)
	require.NoError(t, err)

	require.Equal(t, uint64(1), streamMsgs(t, ns.ClientURL(), "WORK"), "a producer-only instance must submit declared jobs")
}

func streamMsgs(t *testing.T, url, stream string) uint64 {
	t.Helper()
	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	s, err := js.Stream(context.Background(), stream)
	require.NoError(t, err)
	info, err := s.Info(context.Background())
	require.NoError(t, err)
	return info.State.Msgs
}

func TestPublishedConsumers_DropsTheUnmountedTodoConsumer(t *testing.T) {
	all := consumerProviders(&Services{}, discardLogger())
	require.NoError(t, assertConsumerWiring(all), "the full manifest must still be wired")
	if mountTodo {
		return
	}
	require.Empty(t, handlersOf(publishedConsumers(all)), "the todo consumer is declared while todo is not mounted")
}
