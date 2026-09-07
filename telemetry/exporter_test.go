package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTraceExporter_DefaultHTTP(t *testing.T) {
	exp, err := newTraceExporter(context.Background(), OTLPConfig{})
	require.NoError(t, err)
	assert.NotNil(t, exp)
}

func TestNewTraceExporter_HTTPWithOptions(t *testing.T) {
	exp, err := newTraceExporter(context.Background(), OTLPConfig{
		Protocol: "http",
		Endpoint: "otel.example:4318",
		Insecure: true,
		Headers:  map[string]string{"x-token": "v"},
	})
	require.NoError(t, err)
	assert.NotNil(t, exp)
}

func TestNewTraceExporter_GRPC(t *testing.T) {
	exp, err := newTraceExporter(context.Background(), OTLPConfig{
		Protocol: "grpc",
		Endpoint: "otel.example:4317",
		Insecure: true,
		Headers:  map[string]string{"x-token": "v"},
	})
	require.NoError(t, err)
	assert.NotNil(t, exp)
}

func TestNewTraceExporter_UnknownProtocol(t *testing.T) {
	exp, err := newTraceExporter(context.Background(), OTLPConfig{Protocol: "carrier-pigeon"})
	assert.Nil(t, exp)
	assert.ErrorContains(t, err, "unknown otlp protocol")
}

func TestNewMetricExporter_DefaultHTTP(t *testing.T) {
	exp, err := newMetricExporter(context.Background(), OTLPConfig{})
	require.NoError(t, err)
	assert.NotNil(t, exp)
}

func TestNewMetricExporter_HTTPWithOptions(t *testing.T) {
	exp, err := newMetricExporter(context.Background(), OTLPConfig{
		Protocol: "http",
		Endpoint: "otel.example:4318",
		Insecure: true,
		Headers:  map[string]string{"x-token": "v"},
	})
	require.NoError(t, err)
	assert.NotNil(t, exp)
}

func TestNewMetricExporter_GRPC(t *testing.T) {
	exp, err := newMetricExporter(context.Background(), OTLPConfig{
		Protocol: "grpc",
		Endpoint: "otel.example:4317",
		Insecure: true,
		Headers:  map[string]string{"x-token": "v"},
	})
	require.NoError(t, err)
	assert.NotNil(t, exp)
}

func TestNewMetricExporter_UnknownProtocol(t *testing.T) {
	exp, err := newMetricExporter(context.Background(), OTLPConfig{Protocol: "carrier-pigeon"})
	assert.Nil(t, exp)
	assert.ErrorContains(t, err, "unknown otlp protocol")
}
