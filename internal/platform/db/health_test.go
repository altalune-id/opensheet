package db_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
	"altalune.id/template/worker"
)

func testHealthLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func healthCfg() db.HealthConfig {
	return db.HealthConfig{Interval: 30 * time.Second, Timeout: 2 * time.Second}
}

func TestHealthMonitor_NotReadyBeforeFirstProbe(t *testing.T) {
	sqlDB, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
	require.False(t, m.Ready(), "readiness must be false until the first probe lands")
	require.Nil(t, m.Snapshot())
}

func TestHealthMonitor_ProbeSuccess(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
	require.NoError(t, m.Probe(t.Context()))

	require.True(t, m.Ready())
	snap := m.Snapshot()
	require.NotNil(t, snap)
	require.True(t, snap.Ready)
	require.Len(t, snap.Handles, 1, "aliased handles are probed once")
	require.Equal(t, "writer", snap.Handles[0].Name)
	require.True(t, snap.Handles[0].Up)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthMonitor_ProbeFailureClearsReadiness(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing().WillReturnError(context.DeadlineExceeded)

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
	require.Error(t, m.Probe(t.Context()))

	require.False(t, m.Ready())
	snap := m.Snapshot()
	require.NotNil(t, snap)
	require.False(t, snap.Ready)
	require.False(t, snap.Handles[0].Up)
	require.NotEmpty(t, snap.Handles[0].Err)
}

func TestHealthMonitor_ProbesEachDistinctHandle(t *testing.T) {
	writer, wm, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	reader, rm, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	wm.ExpectPing()
	rm.ExpectPing()

	m := db.NewHealthMonitor(db.Pool{W: writer, R: reader}, healthCfg(), testHealthLogger(), nil)
	require.NoError(t, m.Probe(t.Context()))

	snap := m.Snapshot()
	require.Len(t, snap.Handles, 2)
	require.Equal(t, "writer", snap.Handles[0].Name)
	require.Equal(t, "reader", snap.Handles[1].Name)
}

func TestHealthMonitor_IsAStandaloneWorker(t *testing.T) {
	sqlDB, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var w worker.Worker = db.NewHealthMonitor(
		db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
	require.Equal(t, "db-health", w.Name())
}

func TestHealthMonitor_RunProbesOnceBeforeTheFirstTick(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	require.Eventually(t, m.Ready, 2*time.Second, 5*time.Millisecond,
		"Run must probe immediately, not wait a full interval")
	cancel()
	require.NoError(t, <-done, "a canceled context is a clean shutdown, not a worker failure")
}

func TestHealthMonitor_RunSwallowsProbeFailures(t *testing.T) {
	sqlDB, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB},
		db.HealthConfig{Interval: time.Millisecond, Timeout: 50 * time.Millisecond},
		testHealthLogger(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	require.Eventually(t, func() bool {
		snap := m.Snapshot()
		return snap != nil && !snap.Ready
	}, 2*time.Second, 5*time.Millisecond)
	cancel()
	require.NoError(t, <-done,
		"a failing probe must not kill the worker - that is what escalated on every tick before")
}

func TestHealthMonitor_RunClampsNonPositiveConfigToDefaults(t *testing.T) {
	tests := []struct {
		name string
		cfg  db.HealthConfig
	}{
		{"zero config", db.HealthConfig{}},
		{"zero interval only", db.HealthConfig{Timeout: 5 * time.Second}},
		{"zero timeout only", db.HealthConfig{Interval: time.Minute}},
		{"negative interval", db.HealthConfig{Interval: -time.Minute, Timeout: 5 * time.Second}},
		{"negative timeout", db.HealthConfig{Interval: time.Minute, Timeout: -5 * time.Second}},
		{"both negative", db.HealthConfig{Interval: -time.Minute, Timeout: -5 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			mock.ExpectPing()

			m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, tt.cfg, testHealthLogger(), nil)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			require.NotPanics(t, func() { go func() { done <- m.Run(ctx) }() },
				"a non-positive interval must be clamped before it reaches time.NewTicker")

			require.Eventually(t, m.Ready, 2*time.Second, 5*time.Millisecond)
			cancel()
			require.NoError(t, <-done)
		})
	}
}

func TestHealthMonitor_ProbeSucceedsWithZeroConfiguredTimeout(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, db.HealthConfig{}, testHealthLogger(), nil)
	require.NoError(t, m.Probe(t.Context()))
	require.True(t, m.Ready())
	require.NoError(t, mock.ExpectationsWereMet())
}
