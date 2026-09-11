// Package config models opensheet's typed configuration and its viper-driven load path.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/notify"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/logger"
	"altalune.id/opensheet/scheduler"
	"altalune.id/opensheet/telemetry"
)

// Mode selects the deployment posture (selfhosted vs. multi-tenant cloud).
type Mode string

const (
	ModeSelfhosted Mode = "selfhosted"
	ModeCloud      Mode = "cloud"
)

// IsProduction reports whether the mode implies production hardening.
func (m Mode) IsProduction() bool { return m == ModeCloud }

// Config is the top-level typed configuration for opensheet.
type Config struct {
	Mode          Mode                `yaml:"mode"          mapstructure:"mode"          awareness:"required,bootstrap" validate:"required,oneof=selfhosted cloud"`
	HTTP          HTTPConfig          `yaml:"http"          mapstructure:"http"`
	DB            db.DBConfig         `yaml:"db"            mapstructure:"db"`
	Genesis       GenesisConfig       `yaml:"genesis"       mapstructure:"genesis"       awareness:"bootstrap"`
	Onboard       OnboardConfig       `yaml:"onboard"       mapstructure:"onboard"`
	Tenant        TenantConfig        `yaml:"tenant"        mapstructure:"tenant"`
	OIDC          OIDCConfig          `yaml:"oidc"          mapstructure:"oidc"          awareness:"required,mode:cloud"`
	Tokens        tokens.Config       `yaml:"tokens"        mapstructure:"tokens"`
	API           APIConfig           `yaml:"api"           mapstructure:"api"`
	Session       SessionConfig       `yaml:"session"       mapstructure:"session"`
	Log           logger.Config       `yaml:"log"           mapstructure:"log"`
	Telemetry     telemetry.Config    `yaml:"telemetry"     mapstructure:"telemetry"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"     mapstructure:"scheduler"`
	Observability ObservabilityConfig `yaml:"observability" mapstructure:"observability"`
	Mail          MailConfig          `yaml:"mail"          mapstructure:"mail"`
	I18n          I18nConfig          `yaml:"i18n"          mapstructure:"i18n"`
	Compliance    ComplianceConfig    `yaml:"compliance"    mapstructure:"compliance"`
	Security      SecurityConfig      `yaml:"security"      mapstructure:"security"      awareness:"-"`
	Google        GoogleConfig        `yaml:"google"        mapstructure:"google"        awareness:"-"`
	Cache         CacheConfig         `yaml:"cache"         mapstructure:"cache"         awareness:"-"`
	Sheets        SheetsConfig        `yaml:"sheets"        mapstructure:"sheets"        awareness:"-"`
}

// SecurityConfig holds the envelope-encryption key protecting stored third-party credentials.
type SecurityConfig struct {
	EncryptionKey string `yaml:"encryptionKey" mapstructure:"encryptionKey" awareness:"required,secret,bootstrap"`
}

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
}

// ComplianceConfig gates the T&C acceptance flow. When RequireAcceptance is true, signed-in users with no TermsAcceptedAt are redirected to /welcome until they check the box.
type ComplianceConfig struct {
	TermsURL          string `yaml:"termsURL"          mapstructure:"termsURL"          validate:"omitempty,url"`
	PrivacyURL        string `yaml:"privacyURL"        mapstructure:"privacyURL"        validate:"omitempty,url"`
	RequireAcceptance bool   `yaml:"requireAcceptance" mapstructure:"requireAcceptance"`
}

// I18nConfig configures the SSR i18n subsystem.
type I18nConfig struct {
	DefaultLocale string `yaml:"defaultLocale" mapstructure:"defaultLocale" awareness:"required,bootstrap"`
}

// HTTPConfig configures the web-facing HTTP server.
type HTTPConfig struct {
	Addr         string `yaml:"addr"         mapstructure:"addr"         awareness:"required"`
	BasePath     string `yaml:"basePath"     mapstructure:"basePath"`
	BaseURL      string `yaml:"baseURL"      mapstructure:"baseURL"      awareness:"required"                  validate:"omitempty,url"`
	CookieSecure bool   `yaml:"cookieSecure" mapstructure:"cookieSecure"`
	StateSecret  string `yaml:"stateSecret"  mapstructure:"stateSecret"  awareness:"required,secret,bootstrap"`
	RobotsTxt    string `yaml:"robotsTxt"    mapstructure:"robotsTxt"`
}

// GenesisConfig configures the built-in admin account.
type GenesisConfig struct {
	Email      string `yaml:"email"      mapstructure:"email"      awareness:"-"`
	Password   string `yaml:"password"   mapstructure:"password"   awareness:"bootstrap,secret"`
	BreakGlass bool   `yaml:"breakGlass" mapstructure:"breakGlass" awareness:"bootstrap,mode:cloud"`
}

// OnboardConfig configures the first-time setup flow at /onboard.
type OnboardConfig struct {
	SetupToken string `yaml:"setupToken" mapstructure:"setupToken" awareness:"-,secret"`
}

// TenantConfig configures multi-tenant partitioning and RLS-audit inputs.
type TenantConfig struct {
	RLSEnforce              bool               `yaml:"rlsEnforce"              mapstructure:"rlsEnforce"               awareness:"bootstrap"`
	SingletonOrg            SingletonOrgConfig `yaml:"singletonOrg"            mapstructure:"singletonOrg"`
	PersonalOrgSlugFallback string             `yaml:"personalOrgSlugFallback" mapstructure:"personalOrgSlugFallback"`
	PersonalProjectSlug     string             `yaml:"personalProjectSlug"     mapstructure:"personalProjectSlug"`
	TenantScopedTables      []string           `yaml:"tenantScopedTables"      mapstructure:"tenantScopedTables"       awareness:"bootstrap"`
}

// SingletonOrgConfig seeds the first organization created during onboarding. Applies to both modes.
type SingletonOrgConfig struct {
	Slug string `yaml:"slug" mapstructure:"slug" awareness:"bootstrap"`
	Name string `yaml:"name" mapstructure:"name" awareness:"bootstrap"`
}

// OIDCConfig configures the OIDC login flow used by the CLI and web surfaces.
type OIDCConfig struct {
	Issuer             string   `yaml:"issuer"             mapstructure:"issuer"             awareness:"required,mode:cloud" validate:"omitempty,url"`
	ClientID           string   `yaml:"clientID"           mapstructure:"clientID"           awareness:"required,mode:cloud"`
	ClientSecret       string   `yaml:"clientSecret"       mapstructure:"clientSecret"       awareness:"required,mode:cloud,secret"`
	Resource           string   `yaml:"resource"           mapstructure:"resource"           awareness:"-"`
	Scopes             []string `yaml:"scopes"             mapstructure:"scopes"             awareness:"-"`
	RedirectPort       int      `yaml:"redirectPort"       mapstructure:"redirectPort"       awareness:"-" validate:"gte=0,lte=65535"`
	ButtonLabel        string   `yaml:"buttonLabel"        mapstructure:"buttonLabel"        awareness:"-"`
	ButtonLogoURL      string   `yaml:"buttonLogoURL"      mapstructure:"buttonLogoURL"      awareness:"-" validate:"omitempty,url"`
	EndSessionOnLogout bool     `yaml:"endSessionOnLogout" mapstructure:"endSessionOnLogout" awareness:"-"`
}

// APIConfig configures the Connect-RPC API surface.
type APIConfig struct {
	Enabled bool          `yaml:"enabled" mapstructure:"enabled"`
	OpenAPI OpenAPIConfig `yaml:"openapi" mapstructure:"openapi"`
}

// OpenAPIConfig configures the OpenAPI documentation endpoint.
type OpenAPIConfig struct {
	Enabled           bool   `yaml:"enabled"           mapstructure:"enabled"`
	RequireBasicAuth  bool   `yaml:"requireBasicAuth"  mapstructure:"requireBasicAuth"`
	BasicAuthUser     string `yaml:"basicAuthUser"     mapstructure:"basicAuthUser"`
	BasicAuthPassword string `yaml:"basicAuthPassword" mapstructure:"basicAuthPassword" awareness:"secret"`
}

// SessionConfig locates the CLI session cache on disk.
type SessionConfig struct {
	Path string `yaml:"path" mapstructure:"path"`
}

// ObservabilityConfig wires the incident-reporter fan-out sinks.
type ObservabilityConfig struct {
	Reporter notify.Config `yaml:"reporter" mapstructure:"reporter"`
}

// MailConfig configures the transactional mailer. NOTE: mirrors legacy config.MailConfig until the mailer package publishes its own.
type MailConfig struct {
	Driver string       `yaml:"driver" mapstructure:"driver"`
	From   string       `yaml:"from"   mapstructure:"from"`
	SMTP   SMTPConfig   `yaml:"smtp"   mapstructure:"smtp"`
	Resend ResendConfig `yaml:"resend" mapstructure:"resend"`
}

// SMTPConfig configures the SMTP driver used by MailConfig.
type SMTPConfig struct {
	Host string `yaml:"host" mapstructure:"host"`
	Port int    `yaml:"port" mapstructure:"port" validate:"gte=0,lte=65535"`
	User string `yaml:"user" mapstructure:"user"`
	Pass string `yaml:"pass" mapstructure:"pass" awareness:"secret"`
	TLS  bool   `yaml:"tls"  mapstructure:"tls"`
}

// ResendConfig configures the Resend driver used by MailConfig. https://resend.com/docs/api-reference/emails/send-email
type ResendConfig struct {
	APIKey      string `yaml:"apiKey"      mapstructure:"apiKey"      awareness:"secret"`
	Endpoint    string `yaml:"endpoint"    mapstructure:"endpoint"`
	MaxAttempts int    `yaml:"maxAttempts" mapstructure:"maxAttempts" validate:"gte=0,lte=10"`
}

// SchedulerConfig tunes the periodic-job runner. Job cadences are baked into each domain's scheduler adapter, not exposed here.
type SchedulerConfig struct {
	Enabled       bool                          `yaml:"enabled"       mapstructure:"enabled"       awareness:"bootstrap"`
	Timezone      string                        `yaml:"timezone"      mapstructure:"timezone"      awareness:"bootstrap"`
	ShutdownGrace time.Duration                 `yaml:"shutdownGrace" mapstructure:"shutdownGrace" awareness:"-"        validate:"gte=0"`
	Jobs          map[string]SchedulerJobConfig `yaml:"jobs"          mapstructure:"jobs"          awareness:"-"`
}

// SchedulerJobConfig overrides per-job scheduler settings by job name.
type SchedulerJobConfig struct {
	Timezone string `yaml:"timezone" mapstructure:"timezone" awareness:"-"`
}

// GetTimezone resolves Timezone via time.LoadLocation; empty returns time.UTC.
func (s *SchedulerConfig) GetTimezone() (*time.Location, error) {
	if s == nil || s.Timezone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("config: scheduler.timezone %q: %w", s.Timezone, err)
	}
	return loc, nil
}

// Locations resolves every configured zone and returns a per-job lookup falling back to Timezone, then UTC.
func (s *SchedulerConfig) Locations() (scheduler.LocationFunc, error) {
	fallback, err := s.GetTimezone()
	if err != nil {
		return nil, err
	}
	if s == nil || len(s.Jobs) == 0 {
		return scheduler.FixedLocation(fallback), nil
	}
	perJob := make(map[string]*time.Location, len(s.Jobs))
	for name, jc := range s.Jobs {
		if jc.Timezone == "" {
			continue
		}
		loc, lErr := time.LoadLocation(jc.Timezone)
		if lErr != nil {
			return nil, fmt.Errorf("config: scheduler.jobs.%s.timezone %q: %w", name, jc.Timezone, lErr)
		}
		perJob[name] = loc
	}
	return func(jobName string) *time.Location {
		if loc, ok := perJob[jobName]; ok {
			return loc
		}
		return fallback
	}, nil
}

// Validate cascades to each subsystem then runs the struct-tag pass and the cross-field invariants.
func (c *Config) Validate() error {
	c.Mode = Mode(strings.ToLower(strings.TrimSpace(string(c.Mode))))
	if err := c.DB.Validate(); err != nil {
		return err
	}
	if err := c.Log.Validate(); err != nil {
		return err
	}
	if err := c.Telemetry.Validate(); err != nil {
		return err
	}
	if err := validate().Struct(c); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return validateInvariants(c)
}

var v10 = validator.New() //nolint:gochecknoglobals // validator instance is stateless + safe for concurrent use; shared per go-playground/validator conventions.

func validate() *validator.Validate { return v10 }

// validateInvariants enforces conditional and cross-field rules that struct-tag validation can't express — each helper focuses on one rule and emits a specific, actionable error message.
func validateInvariants(c *Config) error {
	if err := validateGenesisPasswordNeedsEmail(c); err != nil {
		return err
	}
	if err := validateGoogleSecretNeedsClientID(c); err != nil {
		return err
	}
	if err := validateCachePostgresNeedsPostgres(c); err != nil {
		return err
	}
	if err := validatePostgresNeedsEncryptionKey(c); err != nil {
		return err
	}
	if err := validateStateSecret(c); err != nil {
		return err
	}
	switch c.Mode {
	case ModeSelfhosted:
		return validateSelfhosted(c)
	case ModeCloud:
		return validateCloud(c)
	}
	return nil
}

func validateSelfhosted(_ *Config) error { return nil }

func validateCloud(c *Config) error {
	if err := validateCloudOIDC(c); err != nil {
		return err
	}
	if err := validateCloudDBDriver(c); err != nil {
		return err
	}
	if err := validateCloudGenesisEmail(c); err != nil {
		return err
	}
	if err := validateCloudGenesisPasswordBreakGlass(c); err != nil {
		return err
	}
	if err := validateCloudSingletonOrg(c); err != nil {
		return err
	}
	if err := validateCloudEncryptionKey(c); err != nil {
		return err
	}
	return validateAutoMigrateNeedsMigrator(c)
}

func validateCloudOIDC(c *Config) error {
	if c.OIDC.Issuer == "" {
		return errors.New("config: mode=cloud requires oidc.issuer (set OPENSHEET_OIDC_ISSUER)")
	}
	if c.OIDC.ClientID == "" {
		return errors.New("config: mode=cloud requires oidc.clientID (set OPENSHEET_OIDC_CLIENT_ID)")
	}
	if c.OIDC.ClientSecret == "" {
		return errors.New("config: mode=cloud requires oidc.clientSecret (set OPENSHEET_OIDC_CLIENT_SECRET)")
	}
	return nil
}

func validateCloudDBDriver(c *Config) error {
	if c.DB.Driver != "" && c.DB.Driver != "postgres" {
		return fmt.Errorf("config: mode=cloud requires db.driver=postgres, got %q (set OPENSHEET_DB_DRIVER=postgres)", c.DB.Driver)
	}
	return nil
}

func validateCloudGenesisEmail(c *Config) error {
	if c.Genesis.Email == "" {
		return errors.New("config: mode=cloud requires genesis.email — first-boot admin identity, matched against OIDC subject email (set OPENSHEET_GENESIS_EMAIL)")
	}
	return nil
}

func validateGenesisPasswordNeedsEmail(c *Config) error {
	if c.Genesis.Password != "" && c.Genesis.Email == "" {
		return errors.New("config: genesis.password without genesis.email — no account is created, so the password is silently ignored (set OPENSHEET_GENESIS_EMAIL, or unset OPENSHEET_GENESIS_PASSWORD)")
	}
	return nil
}

func validateCloudGenesisPasswordBreakGlass(c *Config) error {
	if c.Genesis.Password != "" && !c.Genesis.BreakGlass {
		return errors.New("config: mode=cloud with genesis.password requires genesis.breakGlass=true — the /login local form is hidden in cloud otherwise (set OPENSHEET_GENESIS_BREAK_GLASS=true, or unset OPENSHEET_GENESIS_PASSWORD)")
	}
	return nil
}

func validateCloudSingletonOrg(c *Config) error {
	if c.Tenant.SingletonOrg.Slug == "" {
		return errors.New("config: mode=cloud requires tenant.singletonOrg.slug — the first organization created at bootstrap (set OPENSHEET_TENANT_SINGLETON_ORG_SLUG)")
	}
	if c.Tenant.SingletonOrg.Name == "" {
		return errors.New("config: mode=cloud requires tenant.singletonOrg.name — the display name of the first organization (set OPENSHEET_TENANT_SINGLETON_ORG_NAME)")
	}
	return nil
}

func validateCloudEncryptionKey(c *Config) error {
	if c.Security.EncryptionKey == "" {
		return errors.New("config: mode=cloud requires security.encryptionKey — 32 bytes hex or base64; without it stored Google credentials cannot be read (set OPENSHEET_SECURITY_ENCRYPTION_KEY)")
	}
	return nil
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

func validatePostgresNeedsEncryptionKey(c *Config) error {
	if c.DB.Driver == db.DriverPostgres && c.Security.EncryptionKey == "" {
		return errors.New("config: db.driver=postgres requires security.encryptionKey — 32 bytes hex or base64; without it persisted web sessions cannot be sealed and every login fails (set OPENSHEET_SECURITY_ENCRYPTION_KEY)")
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

func validateAutoMigrateNeedsMigrator(c *Config) error {
	if !c.DB.AllowBypassRLS && c.DB.AutoMigrate && c.DB.Migrator.DSN == "" {
		return errors.New("config: mode=cloud with autoMigrate and RLS enforced requires db.migrator.dsn — run scripts/db/provision.sh (APP=opensheet DB_NAME=opensheet) and set OPENSHEET_DB_MIGRATOR_DSN to the opensheet_migrator credential, or disable autoMigrate and run migrations out-of-band")
	}
	return nil
}
