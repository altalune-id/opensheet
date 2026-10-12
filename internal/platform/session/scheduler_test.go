package session_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/scheduler"
)

type sweepStore struct {
	session.Store
	calls int
	n     int
	err   error
}

func (s *sweepStore) DeleteExpired(context.Context) (int, error) {
	s.calls++
	return s.n, s.err
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestScheduler_JobShape(t *testing.T) {
	jobs := session.NewScheduler(session.NewMemoryStore(), discardLog()).SchedulerJobs()
	require.Len(t, jobs, 1)

	j := jobs[0]
	assert.Equal(t, "session-sweep", j.Name)
	assert.Equal(t, scheduler.ScopeSystem, j.Scope,
		"sessions carry no org_id, so the sweep is not tenant-scoped")
	assert.True(t, j.Singleton, "one replica must own the sweep")
	assert.Equal(t, 2*time.Minute, j.Timeout)
	require.NotNil(t, j.Run)
}

func TestScheduler_RunSweepsExpiredSessions(t *testing.T) {
	store := &sweepStore{n: 3}
	j := session.NewScheduler(store, discardLog()).SchedulerJobs()[0]

	require.NoError(t, j.Run(t.Context()))
	assert.Equal(t, 1, store.calls)
}

func TestScheduler_RunPropagatesStoreFailure(t *testing.T) {
	want := errors.New("boom")
	store := &sweepStore{err: want}
	j := session.NewScheduler(store, discardLog()).SchedulerJobs()[0]

	assert.ErrorIs(t, j.Run(t.Context()), want)
}

func TestScheduler_ScheduleIsAnInterval(t *testing.T) {
	j := session.NewScheduler(session.NewMemoryStore(), discardLog()).SchedulerJobs()[0]
	assert.False(t, scheduler.UsesWallClock(j.Schedule),
		"an interval schedule ignores scheduler.jobs timezone overrides")

	base := time.Date(2026, 1, 1, 7, 15, 0, 0, time.UTC)
	next := j.Schedule.Next(base)
	assert.True(t, next.After(base))
	assert.True(t, next.Sub(base) <= time.Hour+5*time.Minute,
		"an hourly sweep with 5m jitter must fire within 65 minutes, got %v", next.Sub(base))
}
