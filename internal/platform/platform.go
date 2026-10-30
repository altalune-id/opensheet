// Package platform aggregates every cross-cutting primitive the app needs.
package platform

import (
	"errors"
	"io"
	"log/slog"
	"slices"

	"github.com/altalune-id/oidcrp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/mailer"
)

// NanoIDFunc mints a URL-safe nanoid of the requested length.
type NanoIDFunc = func(length int) (string, error)

// Kernel is the platform bag consumed by boot.Server and the composition root.
type Kernel struct {
	Pool     db.Pool
	PgConn   *tenant.PgConn
	Log      *slog.Logger
	Reporter *apperror.Reporter
	Sessions session.Store
	Sealer   sealer.Sealer
	Verifier tokens.Verifier
	Mail     mailer.Mailer
	AltAuth  *oidcrp.Client
	// OIDCStateSecret signs the sign-in state and last-user cookies; never empty while AltAuth is set.
	OIDCStateSecret []byte
	Tracer          trace.Tracer
	Meter           metric.Meter
	Notify          []apperror.ReportSink
	Nano            NanoIDFunc
	Caps            capabilities.Capabilities
	Outbox          outbox.Store
	// Queue is always non-nil: Disabled(log) when queue.enabled is false.
	Queue *queue.Client

	closers []io.Closer
}

// AddCloser registers a resource for reverse-order shutdown by Close.
func (k *Kernel) AddCloser(c io.Closer) {
	if k == nil || c == nil {
		return
	}
	k.closers = append(k.closers, c)
}

// Close closes every registered resource in reverse-creation order and returns errors.Join of anything that failed.
func (k *Kernel) Close() error {
	if k == nil {
		return nil
	}
	var errs []error
	for _, c := range slices.Backward(k.closers) {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
