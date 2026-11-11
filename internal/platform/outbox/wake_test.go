package outbox_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestWakeOnEnqueueWakesTheWorkerOnlyAfterCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		waker := outbox.NewWaker()
		store := outbox.WakeOnEnqueue(fakes.NewOutbox(), waker)
		d := newRecordingDeliverer(nil)
		w := outbox.NewWorker(store, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler),
			outbox.WorkerOpts{Tick: 5 * time.Second, IdlePoll: time.Hour, Waker: waker})

		cancel, done := runWorker(t, w)
		time.Sleep(time.Minute)
		ctx := tenant.Into(t.Context(), tc)

		rolledBack := entry(tc, uuid.New())
		_ = fakes.UnitOfWork(ctx, func(ctx context.Context) error {
			require.NoError(t, store.Enqueue(ctx, rolledBack))
			return errors.New("rolled back")
		})
		time.Sleep(time.Minute)
		assert.Zero(t, d.count(rolledBack.ID), "a rolled-back enqueue woke the worker")

		committed := entry(tc, uuid.New())
		require.NoError(t, fakes.UnitOfWork(ctx, func(ctx context.Context) error {
			require.NoError(t, store.Enqueue(ctx, committed))
			time.Sleep(time.Minute)
			assert.Zero(t, d.count(committed.ID), "the worker woke before the commit")
			return nil
		}))
		time.Sleep(time.Second)
		cancel()
		require.NoError(t, <-done)

		assert.Equal(t, 1, d.count(committed.ID), "a committed enqueue did not wake the worker")
	})
}
