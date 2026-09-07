package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"altalune.id/template/worker"
)

var _ worker.Worker = (*HealthMonitor)(nil)

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

// HealthMonitor probes every distinct handle in a Pool and publishes the latest snapshot.
type HealthMonitor struct {
	pool Pool
	cfg  HealthConfig
	log  *slog.Logger
	snap atomic.Pointer[Health]

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

// Ready reports whether every probed handle responded to the most recent probe; false before the first.
func (m *HealthMonitor) Ready() bool {
	s := m.snap.Load()
	return s != nil && s.Ready
}

// Name identifies the monitor to the worker Supervisor.
func (m *HealthMonitor) Name() string { return "db-health" }

// Run probes once immediately, then every configured interval until ctx is done.
func (m *HealthMonitor) Run(ctx context.Context) error {
	m.probeOnce(ctx)
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			m.probeOnce(ctx)
		}
	}
}

// SECURITY: probe errors never leave Run - a database outage must not kill the process or escalate to the notification sinks on every tick.
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

	m.snap.Store(&Health{At: time.Now().UTC(), Handles: results, Ready: len(failures) == 0})
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
