package config

import (
	"errors"
	"time"

	"altalune.id/opensheet/internal/platform/db"
)

// GoogleConfig configures the Google Sheets integration.
type GoogleConfig struct {
	OAuth   GoogleOAuthConfig  `yaml:"oauth"   mapstructure:"oauth"   awareness:"-"`
	Picker  GooglePickerConfig `yaml:"picker"  mapstructure:"picker"  awareness:"-"`
	Timeout time.Duration      `yaml:"timeout" mapstructure:"timeout" awareness:"-" validate:"gte=0"`
}

// GoogleOAuthConfig holds the OAuth client driving the Google connect flow. NOTE: the requested scope set is a package constant, not a config key.
type GoogleOAuthConfig struct {
	ClientID     string `yaml:"clientID"     mapstructure:"clientID"     awareness:"required,mode:cloud"`
	ClientSecret string `yaml:"clientSecret" mapstructure:"clientSecret" awareness:"required,mode:cloud,secret"`
}

// GooglePickerConfig holds the browser API key used by the Google Picker.
type GooglePickerConfig struct {
	APIKey string `yaml:"apiKey" mapstructure:"apiKey" awareness:"required,mode:cloud"`
}

// CacheDriver selects the backend behind the sheet-data cache.
type CacheDriver string

// The cache drivers.
const (
	CacheDriverAuto     CacheDriver = "auto"
	CacheDriverPostgres CacheDriver = "postgres"
	CacheDriverMemory   CacheDriver = "memory"
)

// CacheConfig configures the sheet-data cache. MaxBytes bounds the memory driver only.
type CacheConfig struct {
	Driver     CacheDriver   `yaml:"driver"     mapstructure:"driver"     awareness:"bootstrap" validate:"omitempty,oneof=auto postgres memory"`
	DefaultTTL time.Duration `yaml:"defaultTTL" mapstructure:"defaultTTL" awareness:"-"         validate:"gte=0"`
	MaxBytes   int64         `yaml:"maxBytes"   mapstructure:"maxBytes"   awareness:"-"         validate:"gte=0"`
}

// Resolve maps CacheDriverAuto onto a concrete driver for the given database driver.
func (c CacheConfig) Resolve(dbDriver db.Driver) CacheDriver {
	if c.Driver != "" && c.Driver != CacheDriverAuto {
		return c.Driver
	}
	if dbDriver == db.DriverPostgres {
		return CacheDriverPostgres
	}
	return CacheDriverMemory
}

// SheetsConfig configures the sheet read/write surface.
type SheetsConfig struct {
	PublicEnabled   bool  `yaml:"publicEnabled"   mapstructure:"publicEnabled"   awareness:"bootstrap"`
	MaxPayloadBytes int64 `yaml:"maxPayloadBytes" mapstructure:"maxPayloadBytes" awareness:"-"         validate:"gte=0"`
	MaxQueryRows    int   `yaml:"maxQueryRows"    mapstructure:"maxQueryRows"    awareness:"-"         validate:"gte=0"`
	MaxSortPages    int   `yaml:"maxSortPages"    mapstructure:"maxSortPages"    awareness:"-"         validate:"gte=0"`
}

func validateOpensheet(c *Config) error {
	if err := validateGoogleSecretNeedsClientID(c); err != nil {
		return err
	}
	if err := validateCachePostgresNeedsPostgres(c); err != nil {
		return err
	}
	return validateStateSecret(c)
}

func validateGoogleSecretNeedsClientID(c *Config) error {
	if c.Google.OAuth.ClientSecret != "" && c.Google.OAuth.ClientID == "" {
		return errors.New("config: google.oauth.clientSecret without google.oauth.clientID — the connect flow is never offered, so the secret is silently ignored (set OPENSHEET_GOOGLE_OAUTH_CLIENT_ID, or unset the secret)")
	}
	return nil
}

func validateCachePostgresNeedsPostgres(c *Config) error {
	if c.Cache.Driver == CacheDriverPostgres && c.DB.Driver == db.DriverSQLite {
		return errors.New("config: cache.driver=postgres requires db.driver=postgres (set OPENSHEET_CACHE_DRIVER=auto)")
	}
	return nil
}

func validateStateSecret(c *Config) error {
	if c.HTTP.StateSecret == "" {
		if c.DB.Driver == db.DriverPostgres {
			return errors.New("config: db.driver=postgres requires http.stateSecret — 32 bytes as hex or base64; without it a fresh key is minted every boot and every persisted web session stops verifying after a restart (set OPENSHEET_HTTP_STATE_SECRET)")
		}
		return nil
	}
	_, err := ParseStateSecret(c.HTTP.StateSecret)
	return err
}
