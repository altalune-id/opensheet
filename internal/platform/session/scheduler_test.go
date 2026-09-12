package session_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/scheduler"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// NOTE: assertSchedulerWiring is positional, so the provider must exist and yield its job for every store.
func TestScheduler_YieldsTheSweepJobForEveryStore(t *testing.T) {
	t.Parallel()
	for _, st := range []session.Store{session.NewMemoryStore(), &countingStore{}} {
		s := session.NewScheduler(st, discardLogger())
		require.NotNil(t, s)
		assert.Len(t, s.SchedulerJobs(), 1)
	}
}

func TestScheduler_SweepJobIsSystemScopedAndSingleton(t *testing.T) {
	t.Parallel()
	s := session.NewScheduler(&countingStore{}, discardLogger())
	jobs := s.SchedulerJobs()
	require.Len(t, jobs, 1)
	j := jobs[0]
	assert.Equal(t, "session-sweep", j.Name)
	assert.Equal(t, scheduler.ScopeSystem, j.Scope)
	assert.True(t, j.Singleton)
	assert.NotZero(t, j.Timeout)
	require.NotNil(t, j.Schedule)
}

func TestScheduler_RunCallsDeleteExpired(t *testing.T) {
	t.Parallel()
	st := &countingStore{}
	jobs := session.NewScheduler(st, discardLogger()).SchedulerJobs()
	require.Len(t, jobs, 1)
	require.NoError(t, jobs[0].Run(context.Background()))
	assert.Equal(t, 1, st.calls)
}

type countingStore struct {
	session.Store
	calls int
}

func (s *countingStore) DeleteExpired(context.Context) (int, error) {
	s.calls++
	return 3, nil
}
