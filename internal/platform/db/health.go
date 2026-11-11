package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	defaultHealthInterval = 30 * time.Second
	defaultHealthTimeout  = 2 * time.Second
)

// Health is an immutable snapshot of one probe round.
type Health struct {
	At      time.Time
	Handles []HandleHealth
	Ready   bool
}

// HandleHealth is one database handle's probe result.
type HandleHealth struct {
	Name    string
	Up      bool
	Latency time.Duration
	Err     string
	Stats   sql.DBStats
}

// HealthMonitor probes every distinct handle in a Pool on demand and caches the latest snapshot for HealthConfig.Interval.
type HealthMonitor struct {
	pool  Pool
	cfg   HealthConfig
	log   *slog.Logger
	snap  atomic.Pointer[Health]
	probe sync.Mutex
	// NOTE: probedAt keeps the monotonic reading that Health.At loses to UTC().
	probedAt atomic.Pointer[time.Time]

	probeDur metric.Float64Histogram
	up       metric.Int64Gauge
	inUse    metric.Int64Gauge
}

// NewHealthMonitor builds the probe. meter may be nil, which disables metrics.
func NewHealthMonitor(pool Pool, cfg HealthConfig, log *slog.Logger, meter metric.Meter) *HealthMonitor {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHealthInterval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHealthTimeout
	}
	m := &HealthMonitor{pool: pool, cfg: cfg, log: log}
	if meter == nil {
		return m
	}
	m.probeDur, _ = meter.Float64Histogram("db.probe_duration_seconds",
		metric.WithDescription("Latency of a database health probe."), metric.WithUnit("s"))
	m.up, _ = meter.Int64Gauge("db.up",
		metric.WithDescription("1 when the database handle responded to its last probe, else 0."))
	m.inUse, _ = meter.Int64Gauge("db.connections_in_use",
		metric.WithDescription("Connections currently in use, per handle."))
	return m
}

// Snapshot returns the most recent probe result, or nil before the first probe.
func (m *HealthMonitor) Snapshot() *Health { return m.snap.Load() }

// Ready reports whether every handle responded to a probe at most HealthConfig.Interval old, probing first when the snapshot is older.
func (m *HealthMonitor) Ready() bool {
	m.probe.Lock()
	defer m.probe.Unlock()
	if at := m.probedAt.Load(); at != nil && time.Since(*at) < m.cfg.Interval {
		return m.snap.Load().Ready
	}
	m.probeOnce(context.Background())
	return m.snap.Load().Ready
}

// SECURITY: probe errors never leave Ready - a database outage must not escalate to the notification sinks on every probe.
func (m *HealthMonitor) probeOnce(ctx context.Context) {
	err := m.Probe(ctx)
	if err == nil || m.log == nil {
		return
	}
	m.log.WarnContext(ctx, "db: health probe failed", slog.String("err", err.Error()))
}

type namedHandle struct {
	name string
	db   *sql.DB
}

func (m *HealthMonitor) handles() []namedHandle {
	out := []namedHandle{{name: "writer", db: m.pool.W}}
	if m.pool.R != nil && m.pool.R != m.pool.W {
		out = append(out, namedHandle{name: "reader", db: m.pool.R})
	}
	return out
}

// Probe pings every distinct handle, publishes a snapshot, and returns the joined failures.
func (m *HealthMonitor) Probe(ctx context.Context) error {
	hs := m.handles()
	results := make([]HandleHealth, 0, len(hs))
	var failures []error

	for _, h := range hs {
		probeCtx, cancel := context.WithTimeout(ctx, m.cfg.Timeout)
		start := time.Now()
		err := h.db.PingContext(probeCtx)
		latency := time.Since(start)
		cancel()

		hh := HandleHealth{Name: h.name, Up: err == nil, Latency: latency, Stats: h.db.Stats()}
		if err != nil {
			hh.Err = err.Error()
			failures = append(failures, fmt.Errorf("db: probe %s: %w", h.name, err))
		}
		results = append(results, hh)
		m.record(ctx, hh)
	}

	now := time.Now()
	m.snap.Store(&Health{At: now.UTC(), Handles: results, Ready: len(failures) == 0})
	m.probedAt.Store(&now)
	if len(failures) > 0 && m.log != nil {
		m.log.WarnContext(ctx, "db: health probe degraded", slog.Int("failed_handles", len(failures)))
	}
	return errors.Join(failures...)
}

func (m *HealthMonitor) record(ctx context.Context, h HandleHealth) {
	if m.probeDur == nil {
		return
	}
	attrs := metric.WithAttributes(attribute.String("handle", h.Name))
	m.probeDur.Record(ctx, h.Latency.Seconds(), attrs)
	var up int64
	if h.Up {
		up = 1
	}
	m.up.Record(ctx, up, attrs)
	m.inUse.Record(ctx, int64(h.Stats.InUse), attrs)
}
