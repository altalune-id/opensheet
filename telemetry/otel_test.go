package telemetry_test

import (
	"context"
	"log/slog"
	"testing"

	"altalune.id/template/telemetry"
)

func TestSetup_DisabledReturnsNoopProviders(t *testing.T) {
	cfg := telemetry.Config{
		Tracing: telemetry.TracingConfig{Enabled: false},
		Metrics: telemetry.MetricsConfig{Enabled: false},
	}
	tp, mp, shutdown, err := telemetry.Setup(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("Setup err = %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	if tp == nil || mp == nil {
		t.Fatal("providers must be non-nil noops")
	}
}

func TestValidate_RejectsBadProtocol(t *testing.T) {
	c := telemetry.Config{OTLP: telemetry.OTLPConfig{Protocol: "carrier-pigeon"}}
	if err := c.Validate(); err == nil {
		t.Error("Validate must reject unknown protocol")
	}
}

func TestNewCounter_Named(t *testing.T) {
	cfg := telemetry.Config{Metrics: telemetry.MetricsConfig{Enabled: true}}
	_, mp, shutdown, err := telemetry.Setup(t.Context(), cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	c := telemetry.NewCounter(mp, "incidents_total")
	if c == nil {
		t.Fatal("NewCounter returned nil")
	}
}
