package sheet_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func startNATSServer(t *testing.T) *server.Server {
	t.Helper()
	opts := &server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(ns.Shutdown)
	return ns
}

type replica struct {
	client *queue.Client
	snaps  sheet.SnapshotStore
}

func startReplica(t *testing.T, url string) replica {
	t.Helper()
	failed := func(_ context.Context, msg string, err error, _ ...any) *apperror.AppError {
		t.Errorf("unexpected report %s: %v", msg, err)
		return nil
	}
	c, err := queue.Connect(t.Context(), queue.Options{
		URL: url, ConnectTimeout: 5 * time.Second, Log: slog.New(slog.DiscardHandler), Unexpected: failed,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	snaps := sheet.NewMemorySnapshotStoreForTest(1 << 20)
	ls := sheet.NewSnapshotListener(snaps).Listeners()
	require.NoError(t, c.Declare(nil, []queue.Broadcast{ls[0].Broadcast}))
	seq, err := c.BroadcastStartSeq(t.Context())
	require.NoError(t, err)
	l, err := queue.NewListener(c, ls, seq)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("listener did not stop")
		}
	})
	return replica{client: c, snaps: snaps}
}

func TestSnapshotInvalidated_EveryReplicaDropsItsMemoryEntries(t *testing.T) {
	ns := startNATSServer(t)
	a, b := startReplica(t, ns.ClientURL()), startReplica(t, ns.ClientURL())
	store := fakes.NewSheet()
	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()})
	tc, err := tenant.From(ctx)
	require.NoError(t, err)
	sh := savedSheet(t, store, tc.OrgID, tc.ProjectID)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
	seedMemory(t, a.snaps, key, "e")
	seedMemory(t, b.snaps, key, "e")

	svc, _, _ := newSvcGoogle(t, store, a.snaps, sheet.NewMemoryIdempotencyStore(), false, sheet.WithQueue(a.client))
	require.NoError(t, svc.PurgeCache(ctx, sh.ID))

	require.Eventually(t, func() bool { return !cached(t, a.snaps, key) && !cached(t, b.snaps, key) },
		5*time.Second, 10*time.Millisecond, "the other replica must drop its stale snapshot once any replica purges")
}
