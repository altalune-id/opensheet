package telemetry_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"altalune.id/template/telemetry"
)

func TestConfig_Validate_Valid(t *testing.T) {
	cases := []telemetry.Config{
		{},
		{OTLP: telemetry.OTLPConfig{Protocol: "http"}},
		{OTLP: telemetry.OTLPConfig{Protocol: "grpc"}},
		{Tracing: telemetry.TracingConfig{SampleRatio: 0}},
		{Tracing: telemetry.TracingConfig{SampleRatio: 0.25}},
		{Tracing: telemetry.TracingConfig{SampleRatio: 1}},
	}
	for _, c := range cases {
		t.Run(c.OTLP.Protocol, func(t *testing.T) {
			assert.NoError(t, c.Validate())
		})
	}
}

func TestConfig_Validate_BadProtocol(t *testing.T) {
	c := telemetry.Config{OTLP: telemetry.OTLPConfig{Protocol: "carrier-pigeon"}}
	err := c.Validate()
	assert.ErrorContains(t, err, "must be http or grpc")
}

func TestConfig_Validate_BadSampleRatio(t *testing.T) {
	cases := []float64{-0.01, -1, 1.01, 2}
	for _, r := range cases {
		c := telemetry.Config{Tracing: telemetry.TracingConfig{SampleRatio: r}}
		err := c.Validate()
		assert.ErrorContains(t, err, "sampleRatio")
	}
}
