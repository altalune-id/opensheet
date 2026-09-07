// Package config models altempl's typed configuration and its viper-driven load path.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/notify"
	"altalune.id/template/internal/platform/tokens"
	"altalune.id/template/logger"
	"altalune.id/template/scheduler"
	"altalune.id/template/telemetry"
)

// Mode selects the deployment posture (selfhosted vs. multi-tenant cloud).
type Mode string

const (
	ModeSelfhosted Mode = "selfhosted"
	ModeCloud      Mode = "cloud"
)

// IsProduction reports whether the mode implies production hardening.
func (m Mode) IsProduction() bool { return m == ModeCloud }

// Config is the top-level typed configuration for altempl.
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
	return validateAutoMigrateNeedsMigrator(c)
}

func validateCloudOIDC(c *Config) error {
	if c.OIDC.Issuer == "" {
		return errors.New("config: mode=cloud requires oidc.issuer (set ALT_OIDC_ISSUER)")
	}
	if c.OIDC.ClientID == "" {
		return errors.New("config: mode=cloud requires oidc.clientID (set ALT_OIDC_CLIENT_ID)")
	}
	if c.OIDC.ClientSecret == "" {
		return errors.New("config: mode=cloud requires oidc.clientSecret (set ALT_OIDC_CLIENT_SECRET)")
	}
	return nil
}

func validateCloudDBDriver(c *Config) error {
	if c.DB.Driver != "" && c.DB.Driver != "postgres" {
		return fmt.Errorf("config: mode=cloud requires db.driver=postgres, got %q (set ALT_DB_DRIVER=postgres)", c.DB.Driver)
	}
	return nil
}

func validateCloudGenesisEmail(c *Config) error {
	if c.Genesis.Email == "" {
		return errors.New("config: mode=cloud requires genesis.email — first-boot admin identity, matched against OIDC subject email (set ALT_GENESIS_EMAIL)")
	}
	return nil
}

func validateGenesisPasswordNeedsEmail(c *Config) error {
	if c.Genesis.Password != "" && c.Genesis.Email == "" {
		return errors.New("config: genesis.password without genesis.email — no account is created, so the password is silently ignored (set ALT_GENESIS_EMAIL, or unset ALT_GENESIS_PASSWORD)")
	}
	return nil
}

func validateCloudGenesisPasswordBreakGlass(c *Config) error {
	if c.Genesis.Password != "" && !c.Genesis.BreakGlass {
		return errors.New("config: mode=cloud with genesis.password requires genesis.breakGlass=true — the /login local form is hidden in cloud otherwise (set ALT_GENESIS_BREAK_GLASS=true, or unset ALT_GENESIS_PASSWORD)")
	}
	return nil
}

func validateCloudSingletonOrg(c *Config) error {
	if c.Tenant.SingletonOrg.Slug == "" {
		return errors.New("config: mode=cloud requires tenant.singletonOrg.slug — the first organization created at bootstrap (set ALT_TENANT_SINGLETON_ORG_SLUG)")
	}
	if c.Tenant.SingletonOrg.Name == "" {
		return errors.New("config: mode=cloud requires tenant.singletonOrg.name — the display name of the first organization (set ALT_TENANT_SINGLETON_ORG_NAME)")
	}
	return nil
}

func validateAutoMigrateNeedsMigrator(c *Config) error {
	if !c.DB.AllowBypassRLS && c.DB.AutoMigrate && c.DB.Migrator.DSN == "" {
		return errors.New("config: mode=cloud with autoMigrate and RLS enforced requires db.migrator.dsn — run scripts/db/provision.sh (APP=altempl DB_NAME=altempl) and set ALT_DB_MIGRATOR_DSN to the altempl_migrator credential, or disable autoMigrate and run migrations out-of-band")
	}
	return nil
}
