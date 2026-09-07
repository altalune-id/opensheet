package telemetry

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// NewCounter constructs an Int64Counter on the given MeterProvider.
func NewCounter(mp metric.MeterProvider, name string) metric.Int64Counter {
	m := mp.Meter("altalune.id/template")
	c, err := m.Int64Counter(name)
	if err != nil {
		slog.Warn("telemetry.counter", "name", name, "error", err)
		n, _ := noop.NewMeterProvider().Meter("altalune.id/template").Int64Counter(name)
		return n
	}
	return c
}
