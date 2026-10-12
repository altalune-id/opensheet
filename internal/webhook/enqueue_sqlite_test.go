package webhook_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/webhook"
)

func unexpectedFails(t *testing.T) apperror.UnexpectedFunc {
	t.Helper()
	return func(_ context.Context, msg string, err error, _ ...any) *apperror.AppError {
		t.Errorf("unexpected report %q: %v", msg, err)
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal)
	}
}

func TestSQLite_EnqueueWritesRowsForTheCallerProjectOnly(t *testing.T) {
	store, sqlDB, tc := newSQLiteStore(t)
	cfg := db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix}
	pool := db.Pool{W: sqlDB, R: sqlDB}
	ob := outbox.NewStore(cfg, pool, nil)
	uow := tenant.NewUnitOfWork(cfg, pool, nil)
	slugs := &slugStub{slug: "altalune"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := webhook.NewService(store, log, unexpectedFails(t), newSealer(t), ob, slugs)

	ctxP := tenant.Into(t.Context(), tc)
	e, _, err := svc.Create(ctxP, validURL, "", []events.Type{events.PostPublished})
	require.NoError(t, err)

	enqueue := func(ctx context.Context) error {
		return uow(ctx, func(ctx context.Context) error {
			return svc.Enqueue(ctx, events.PostPublished, postPublished())
		})
	}
	require.NoError(t, enqueue(ctxP))

	rows, err := ob.ListByTarget(ctxP, e.ID.String(), outbox.MaxListLimit)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, tc.ProjectID, rows[0].ProjectID)
	assert.Equal(t, tc.OrgID, rows[0].OrgID)
	assert.Equal(t, "blog.post.published", decodeEnvelope(t, rows[0].Payload)["type"])

	q := tc
	q.ProjectID = seedProject(t, sqlDB, tc)
	require.NoError(t, enqueue(tenant.Into(t.Context(), q)))

	rows, err = ob.ListByTarget(ctxP, e.ID.String(), outbox.MaxListLimit)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "project Q must not reach project P's endpoint")
	assert.Equal(t, 1, slugs.calls)
}
