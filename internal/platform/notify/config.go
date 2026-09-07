package notify

// Config wires ReportSinks that fan-out incidents from apperror.Reporter.
type Config struct {
	MinSeverity string       `yaml:"minSeverity" mapstructure:"minSeverity"`
	Sinks       []SinkConfig `yaml:"sinks"       mapstructure:"sinks"`
}

// SinkConfig describes one sink adapter to build.
type SinkConfig struct {
	Kind       string   `yaml:"kind"       mapstructure:"kind"`
	WebhookURL string   `yaml:"webhookUrl" mapstructure:"webhookUrl" awareness:"secret"`
	To         []string `yaml:"to"         mapstructure:"to"`
	From       string   `yaml:"from"       mapstructure:"from"`
	Subject    string   `yaml:"subject"    mapstructure:"subject"`
}
