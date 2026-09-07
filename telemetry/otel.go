package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Setup wires OTel tracer + meter providers per cfg and returns a shutdown func that drains both.
func Setup(ctx context.Context, cfg Config, log *slog.Logger) (trace.TracerProvider, metric.MeterProvider, func(context.Context) error, error) {
	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(semconv.ServiceName("altempl")))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("telemetry: resource: %w", err)
	}

	var tp trace.TracerProvider = tracenoop.NewTracerProvider()
	var mp metric.MeterProvider = noop.NewMeterProvider()
	shutdowns := []func(context.Context) error{}

	if cfg.Tracing.Enabled {
		exp, err := newTraceExporter(ctx, cfg.OTLP)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("telemetry: trace exporter: %w", err)
		}
		sample := sdktrace.TraceIDRatioBased(cfg.Tracing.SampleRatio)
		if cfg.Tracing.SampleRatio <= 0 {
			sample = sdktrace.NeverSample()
		} else if cfg.Tracing.SampleRatio >= 1 {
			sample = sdktrace.AlwaysSample()
		}
		stp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sample)),
		)
		tp = stp
		shutdowns = append(shutdowns, stp.Shutdown)
	}

	if cfg.Metrics.Enabled {
		readers := []sdkmetric.Reader{}
		if cfg.OTLP.Endpoint != "" {
			exp, err := newMetricExporter(ctx, cfg.OTLP)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("telemetry: metric exporter: %w", err)
			}
			interval := 30 * time.Second
			if cfg.Metrics.ExportInterval != "" {
				if d, err := time.ParseDuration(cfg.Metrics.ExportInterval); err == nil {
					interval = d
				}
			}
			readers = append(readers, sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval)))
		}
		if cfg.Metrics.Prometheus.Enabled {
			promExp, err := prometheus.New()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("telemetry: prom exporter: %w", err)
			}
			readers = append(readers, promExp)
		}
		smpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
		for _, r := range readers {
			smpOpts = append(smpOpts, sdkmetric.WithReader(r))
		}
		smp := sdkmetric.NewMeterProvider(smpOpts...)
		mp = smp
		shutdowns = append(shutdowns, smp.Shutdown)
	}

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	shutdown := func(ctx context.Context) error {
		var firstErr error
		for _, s := range shutdowns {
			if err := s(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			log.WarnContext(ctx, "telemetry.shutdown", "error", firstErr)
		}
		return firstErr
	}
	return tp, mp, shutdown, nil
}
