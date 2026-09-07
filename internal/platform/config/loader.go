package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Option customizes Load's behavior.
type Option func(*loadOptions)

type loadOptions struct {
	requireFile bool
}

// WithRequireFile makes Load fail when the config file is missing.
func WithRequireFile() Option { return func(o *loadOptions) { o.requireFile = true } }

// Load resolves defaults <- yaml file <- ALT_* env vars (last wins) into a typed Config.
func Load(path string, opts ...Option) (*Config, error) {
	o := loadOptions{}
	for _, opt := range opts {
		opt(&o)
	}

	v := viper.NewWithOptions(viper.KeyDelimiter("."))

	setDefaults(v)

	var cfg Config
	bindEnv(v, "", reflect.TypeOf(cfg))

	v.SetEnvPrefix("ALT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigType("yaml")
		v.SetConfigName("altempl")
		v.AddConfigPath(".")
		if home, err := os.UserHomeDir(); err == nil {
			v.AddConfigPath(home)
		}
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			if o.requireFile {
				return nil, fmt.Errorf("config: file required but not found (path=%q)", path)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("config: read %q: %w", path, err)
		} else if o.requireFile {
			return nil, fmt.Errorf("config: file required but not found (path=%q): %w", path, err)
		}
	}

	decode := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))
	if err := v.Unmarshal(&cfg, decode); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("mode", string(ModeSelfhosted))

	v.SetDefault("http.addr", ":5150")
	v.SetDefault("http.cookieSecure", false)

	v.SetDefault("db.driver", "sqlite")
	v.SetDefault("db.dsn", filepath.Join(homeDir(), ".altempl", "altempl.db"))
	v.SetDefault("db.autoMigrate", true)
	v.SetDefault("db.schema", "public")
	v.SetDefault("db.tablePrefix", "altempl_")
	v.SetDefault("db.connectTimeout", "30s")
	v.SetDefault("db.connectBackoff", "250ms")
	v.SetDefault("db.health.interval", "30s")
	v.SetDefault("db.health.timeout", "2s")

	v.SetDefault("scheduler.enabled", true)
	v.SetDefault("scheduler.timezone", "UTC")
	v.SetDefault("scheduler.shutdownGrace", "30s")

	v.SetDefault("api.enabled", true)
	v.SetDefault("api.openapi.enabled", true)
	v.SetDefault("api.openapi.requireBasicAuth", true)

	v.SetDefault("session.path", filepath.Join(homeDir(), ".altempl", "session.json"))

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

	v.SetDefault("telemetry.tracing.enabled", false)
	v.SetDefault("telemetry.metrics.enabled", false)
	v.SetDefault("telemetry.metrics.exportInterval", "30s")
	v.SetDefault("telemetry.metrics.prometheus.enabled", false)
	v.SetDefault("telemetry.metrics.prometheus.addr", ":9091")
	v.SetDefault("telemetry.metrics.prometheus.path", "/metrics")

	v.SetDefault("observability.reporter.minSeverity", "error")

	v.SetDefault("mail.driver", "console")
	v.SetDefault("mail.from", "no-reply@altempl.local")
	v.SetDefault("mail.smtp.port", 587)
	v.SetDefault("mail.smtp.tls", true)
	v.SetDefault("mail.resend.maxAttempts", 3)

	v.SetDefault("tenant.rlsEnforce", true)
	v.SetDefault("tenant.personalOrgSlugFallback", "personal")
	v.SetDefault("tenant.personalProjectSlug", "default")
	v.SetDefault("tenant.singletonOrg.slug", "default")
	v.SetDefault("tenant.singletonOrg.name", "Default Organization")

	v.SetDefault("genesis.breakGlass", false)

	v.SetDefault("oidc.scopes", []string{"openid", "email", "profile"})

	v.SetDefault("i18n.defaultLocale", "en-US")

	v.SetDefault("tokens.audience", "urn:altempl:api")
	v.SetDefault("tokens.supportedAlgs", []string{"RS256", "ES256"})
	v.SetDefault("tokens.clockSkew", "60s")
}

// Defaults returns a Config with the seeded defaults materialised as a typed value.
func Defaults() *Config {
	v := viper.NewWithOptions(viper.KeyDelimiter("."))
	setDefaults(v)
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		panic(fmt.Errorf("config: Defaults unmarshal: %w", err))
	}
	return &cfg
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}
