package telemetry_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/telemetry"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSetup_TracingEnabledSampleZero(t *testing.T) {
	cfg := telemetry.Config{
		OTLP: telemetry.OTLPConfig{Protocol: "http", Insecure: true, Endpoint: "127.0.0.1:1"},
		Tracing: telemetry.TracingConfig{
			Enabled:     true,
			SampleRatio: 0,
		},
	}
	tp, mp, shutdown, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, mp)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestSetup_TracingEnabledSampleOne(t *testing.T) {
	cfg := telemetry.Config{
		OTLP:    telemetry.OTLPConfig{Protocol: "http", Insecure: true, Endpoint: "127.0.0.1:1"},
		Tracing: telemetry.TracingConfig{Enabled: true, SampleRatio: 1},
	}
	tp, _, shutdown, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, tp)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestSetup_TracingEnabledSampleFraction(t *testing.T) {
	cfg := telemetry.Config{
		OTLP:    telemetry.OTLPConfig{Protocol: "http", Insecure: true, Endpoint: "127.0.0.1:1"},
		Tracing: telemetry.TracingConfig{Enabled: true, SampleRatio: 0.5},
	}
	_, _, shutdown, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestSetup_TracingExporterErrorPropagates(t *testing.T) {
	cfg := telemetry.Config{
		OTLP:    telemetry.OTLPConfig{Protocol: "carrier-pigeon"},
		Tracing: telemetry.TracingConfig{Enabled: true, SampleRatio: 0.5},
	}
	_, _, _, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.Error(t, err)
	assert.ErrorContains(t, err, "trace exporter")
}

func TestSetup_MetricsWithPrometheusOnly(t *testing.T) {
	cfg := telemetry.Config{
		Metrics: telemetry.MetricsConfig{
			Enabled:    true,
			Prometheus: telemetry.PrometheusConfig{Enabled: true},
		},
	}
	_, mp, shutdown, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, mp)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestSetup_MetricsWithOTLPAndInterval(t *testing.T) {
	cfg := telemetry.Config{
		OTLP: telemetry.OTLPConfig{Protocol: "http", Insecure: true, Endpoint: "127.0.0.1:1"},
		Metrics: telemetry.MetricsConfig{
			Enabled:        true,
			ExportInterval: "15s",
		},
	}
	_, mp, shutdown, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, mp)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestSetup_MetricsInvalidIntervalIgnored(t *testing.T) {
	cfg := telemetry.Config{
		OTLP: telemetry.OTLPConfig{Protocol: "http", Insecure: true, Endpoint: "127.0.0.1:1"},
		Metrics: telemetry.MetricsConfig{
			Enabled:        true,
			ExportInterval: "not-a-duration",
		},
	}
	_, _, shutdown, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestSetup_MetricsExporterErrorPropagates(t *testing.T) {
	cfg := telemetry.Config{
		OTLP:    telemetry.OTLPConfig{Protocol: "carrier-pigeon", Endpoint: "otel.example"},
		Metrics: telemetry.MetricsConfig{Enabled: true},
	}
	_, _, _, err := telemetry.Setup(context.Background(), cfg, discardLogger())
	require.Error(t, err)
	assert.ErrorContains(t, err, "metric exporter")
}

func TestPrometheusHandler_ServesMetrics(t *testing.T) {
	h := telemetry.PrometheusHandler()
	require.NotNil(t, h)

	srv := httptest.NewServer(h)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(srv.URL)
	require.NoError(t, err)
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.NotEmpty(t, body)
}
