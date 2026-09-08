package sheet_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/scheduler"
)

func TestScheduler_JobMetadata(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewSheet(), false)

	jobs := sheet.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).SchedulerJobs()

	require.Len(t, jobs, 1)
	j := jobs[0]
	assert.Equal(t, "sheet-write-attempt-sweep", j.Name)
	assert.Equal(t, scheduler.ScopeTenant, j.Scope, "the sweep runs per tenant, so RLS scopes the DELETE")
	assert.True(t, j.Singleton)
	assert.Positive(t, j.Timeout, "an unbounded sweep can wedge the runner")
	require.NotNil(t, j.Schedule)
	require.NotNil(t, j.Run)
}

func TestScheduler_RunSweepsThroughTheService(t *testing.T) {
	t.Parallel()
	attempts := sheet.NewMemoryIdempotencyStore()
	svc, _ := newSvcAttempts(t, fakes.NewSheet(), fakes.NewSheetSnapshots(), attempts, false)
	ctx, _ := tenantCtx(t)

	k := sheet.IdempotencyKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Rates", Key: "idem-1"}
	_, _, err := attempts.Reserve(ctx, k, "hash-a", -time.Second)
	require.NoError(t, err)

	job := sheet.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).SchedulerJobs()[0]
	require.NoError(t, job.Run(ctx))

	claimed, _, err := attempts.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	assert.True(t, claimed, "the job must have swept the expired attempt through Service.SweepWriteAttempts")
}

func TestScheduler_RunWithoutTenantScopeFails(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewSheet(), false)

	job := sheet.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).SchedulerJobs()[0]

	require.Error(t, job.Run(t.Context()), "a ScopeTenant job must refuse an unscoped context")
}
