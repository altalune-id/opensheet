package todo_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/todo"
	"altalune.id/template/scheduler"
)

func TestScheduler_JobShape(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	s := todo.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), scheduler.FixedLocation(time.UTC))
	jobs := s.SchedulerJobs()
	require.Len(t, jobs, 1)

	j := jobs[0]
	require.Equal(t, "todo-autocomplete-stale", j.Name)
	require.Equal(t, scheduler.ScopeTenant, j.Scope)
	require.True(t, j.Singleton, "a cross-tenant sweep must run on one replica only")
	require.Equal(t, 2*time.Minute, j.Timeout)
	require.NotNil(t, j.Run)
}

func TestScheduler_ScheduleFiresEverySixHours(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	s := todo.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), scheduler.FixedLocation(time.UTC))
	j := s.SchedulerJobs()[0]
	got := j.Schedule.Next(time.Date(2026, 1, 1, 7, 15, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), got)
}

func TestScheduler_HonoursPerJobTimezone(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)

	loc := func(jobName string) *time.Location {
		require.Equal(t, "todo-autocomplete-stale", jobName, "the provider must resolve its own job name")
		return jkt
	}

	svc, _ := newSvc(t, fakes.NewTodo())
	s := todo.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), loc)
	j := s.SchedulerJobs()[0]
	require.Contains(t, j.Schedule.String(), "Asia/Jakarta")
}

func TestScheduler_NilLocationFuncDefaultsToUTC(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	s := todo.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	j := s.SchedulerJobs()[0]
	require.Contains(t, j.Schedule.String(), "UTC")
}

func TestScheduler_RunSweepsTheTenantInContext(t *testing.T) {
	var gotOrg uuid.UUID
	store := fakes.NewTodo()
	store.MarkDoneOlderThanFn = func(_ context.Context, org uuid.UUID, _ time.Time, _ int) (int, error) {
		gotOrg = org
		return 3, nil
	}
	svc, _ := newSvc(t, store)
	s := todo.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), scheduler.FixedLocation(time.UTC))
	j := s.SchedulerJobs()[0]

	ctx, tc := tenantCtx(t)
	require.NoError(t, j.Run(ctx))
	require.Equal(t, tc.OrgID, gotOrg)
}

func TestScheduler_RunWithoutTenantFails(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	s := todo.NewScheduler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), scheduler.FixedLocation(time.UTC))
	j := s.SchedulerJobs()[0]
	require.Error(t, j.Run(context.Background()))
}
