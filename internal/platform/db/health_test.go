package db_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
)

func testHealthLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func healthCfg() db.HealthConfig {
	return db.HealthConfig{Interval: 30 * time.Second, Timeout: 2 * time.Second}
}

func TestHealthMonitor_NoSnapshotBeforeFirstProbe(t *testing.T) {
	sqlDB, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
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

func TestHealthMonitor_ReadyProbesOnlyWhenTheSnapshotIsStale(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()

	interval := 200 * time.Millisecond
	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB},
		db.HealthConfig{Interval: interval, Timeout: time.Second}, testHealthLogger(), nil)

	require.True(t, m.Ready(), "the first probe request must ping")
	time.Sleep(interval / 2)
	require.True(t, m.Ready(), "a fresh snapshot must answer without a ping")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectPing().WillReturnError(context.DeadlineExceeded)
	time.Sleep(interval)
	require.False(t, m.Ready(), "a stale snapshot must ping again")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthMonitor_SendsNoQueryWithoutAProbe(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB},
		db.HealthConfig{Interval: time.Millisecond, Timeout: time.Second}, testHealthLogger(), nil)
	require.NoError(t, m.Probe(t.Context()))
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, mock.ExpectationsWereMet())
	require.True(t, m.Snapshot().Ready, "an unrequested ping hit the database")
}

func TestHealthMonitor_ClampsNonPositiveConfigToDefaults(t *testing.T) {
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
			require.True(t, m.Ready())
			require.True(t, m.Ready(), "a non-positive interval must clamp to the default cache age")
			require.NoError(t, mock.ExpectationsWereMet())
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

func TestHealthMonitor_CacheAgeIgnoresWallClockJumps(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mock.ExpectPing()

	m := db.NewHealthMonitor(db.Pool{W: sqlDB, R: sqlDB}, healthCfg(), testHealthLogger(), nil)
	require.True(t, m.Ready())
	snap := m.Snapshot()
	snap.At = snap.At.Add(-time.Hour)

	require.True(t, m.Ready())
	require.NoError(t, mock.ExpectationsWereMet(), "a wall-clock jump made a fresh probe look stale")
}
