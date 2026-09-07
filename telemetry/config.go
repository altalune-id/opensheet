package telemetry

import "fmt"

// Config groups OTel + Prometheus telemetry settings.
type Config struct {
	OTLP    OTLPConfig    `yaml:"otlp"    mapstructure:"otlp"`
	Tracing TracingConfig `yaml:"tracing" mapstructure:"tracing"`
	Metrics MetricsConfig `yaml:"metrics" mapstructure:"metrics"`
}

// OTLPConfig configures the OTLP exporter transport.
type OTLPConfig struct {
	Endpoint string            `yaml:"endpoint" mapstructure:"endpoint"`
	Protocol string            `yaml:"protocol" mapstructure:"protocol"`
	Insecure bool              `yaml:"insecure" mapstructure:"insecure"`
	Headers  map[string]string `yaml:"headers"  mapstructure:"headers"  awareness:"secret"`
}

// TracingConfig toggles tracing and sets its head sample ratio.
type TracingConfig struct {
	Enabled     bool    `yaml:"enabled"     mapstructure:"enabled"`
	SampleRatio float64 `yaml:"sampleRatio" mapstructure:"sampleRatio"`
}

// MetricsConfig configures the meter provider and its readers.
type MetricsConfig struct {
	Enabled        bool             `yaml:"enabled"        mapstructure:"enabled"`
	ExportInterval string           `yaml:"exportInterval" mapstructure:"exportInterval"`
	Prometheus     PrometheusConfig `yaml:"prometheus"     mapstructure:"prometheus"`
}

// PrometheusConfig configures the Prometheus /metrics endpoint.
type PrometheusConfig struct {
	Enabled bool   `yaml:"enabled" mapstructure:"enabled"`
	Addr    string `yaml:"addr"    mapstructure:"addr"`
	Path    string `yaml:"path"    mapstructure:"path"`
}

// Validate rejects invalid telemetry settings before Setup runs.
func (c *Config) Validate() error {
	if c.OTLP.Protocol != "" && c.OTLP.Protocol != "http" && c.OTLP.Protocol != "grpc" {
		return fmt.Errorf("telemetry.otlp.protocol %q — must be http or grpc", c.OTLP.Protocol)
	}
	if c.Tracing.SampleRatio < 0 || c.Tracing.SampleRatio > 1 {
		return fmt.Errorf("telemetry.tracing.sampleRatio %v — must be in [0,1]", c.Tracing.SampleRatio)
	}
	return nil
}
