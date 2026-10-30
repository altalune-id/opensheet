// Package config models opensheet's typed configuration and its viper-driven load path.
package config

import (
	"errors"
	"fmt"
	"net/url"
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
	MCP           MCPConfig           `yaml:"mcp"           mapstructure:"mcp"`
	Session       SessionConfig       `yaml:"session"       mapstructure:"session"`
	Log           logger.Config       `yaml:"log"           mapstructure:"log"`
	Telemetry     telemetry.Config    `yaml:"telemetry"     mapstructure:"telemetry"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"     mapstructure:"scheduler"`
	Queue         QueueConfig         `yaml:"queue"         mapstructure:"queue"`
	Observability ObservabilityConfig `yaml:"observability" mapstructure:"observability"`
	Mail          MailConfig          `yaml:"mail"          mapstructure:"mail"`
	I18n          I18nConfig          `yaml:"i18n"          mapstructure:"i18n"`
	Compliance    ComplianceConfig    `yaml:"compliance"    mapstructure:"compliance"`
	Security      SecurityConfig      `yaml:"security"      mapstructure:"security"`
	Blog          BlogConfig          `yaml:"blog"          mapstructure:"blog"`
	DataPlane     DataPlaneConfig     `yaml:"dataplane"     mapstructure:"dataplane"`
	Google        GoogleConfig        `yaml:"google"        mapstructure:"google"        awareness:"-"`
	Cache         CacheConfig         `yaml:"cache"         mapstructure:"cache"         awareness:"-"`
	Sheets        SheetsConfig        `yaml:"sheets"        mapstructure:"sheets"        awareness:"-"`
}

// BlogConfig gates the blog data plane's uncredentialed reads.
type BlogConfig struct {
	// SECURITY: when true an anonymous caller may read PUBLISHED posts over S3; drafts stay 404.
	PublicReads bool `yaml:"publicReads" mapstructure:"publicReads" awareness:"-"`
}

// DataPlaneConfig gates S3, the REST data plane.
type DataPlaneConfig struct {
	Enabled bool `yaml:"enabled" mapstructure:"enabled" awareness:"-"`
}

// SecurityConfig holds the key that seals secrets at rest.
type SecurityConfig struct {
	// SECURITY: 32 bytes as hex or standard base64; seals the session Principal, which carries a live IdP ID token.
	EncryptionKey string `yaml:"encryptionKey" mapstructure:"encryptionKey" awareness:"required,secret,bootstrap"`
}

// ComplianceConfig gates the T&C acceptance flow.
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
	Addr         string    `yaml:"addr"         mapstructure:"addr"         awareness:"required"`
	BasePath     string    `yaml:"basePath"     mapstructure:"basePath"`
	BaseURL      string    `yaml:"baseURL"      mapstructure:"baseURL"      awareness:"required"                  validate:"omitempty,url"`
	CookieSecure bool      `yaml:"cookieSecure" mapstructure:"cookieSecure"`
	StateSecret  string    `yaml:"stateSecret"  mapstructure:"stateSecret"  awareness:"required,secret,bootstrap"`
	RobotsTxt    string    `yaml:"robotsTxt"    mapstructure:"robotsTxt"`
	CSP          CSPConfig `yaml:"csp"          mapstructure:"csp"`
}

// CSPConfig configures the Content-Security-Policy response header.
type CSPConfig struct {
	// SECURITY: disabling this lets markup injected into user content execute in a viewer's session.
	Enabled bool `yaml:"enabled"    mapstructure:"enabled"    awareness:"required"`
	// ReportOnly logs violations without blocking.
	ReportOnly bool   `yaml:"reportOnly" mapstructure:"reportOnly" awareness:"-"`
	ReportURI  string `yaml:"reportURI"  mapstructure:"reportURI"  awareness:"-"                validate:"omitempty,uri"`
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
	PersonalProjectSlug     string             `yaml:"personalProjectSlug"     mapstructure:"personalProjectSlug"      awareness:"-"`
	TenantScopedTables      []string           `yaml:"tenantScopedTables"      mapstructure:"tenantScopedTables"       awareness:"bootstrap"`
}

// SingletonOrgConfig seeds the first organization created during onboarding.
type SingletonOrgConfig struct {
	Slug string `yaml:"slug" mapstructure:"slug" awareness:"bootstrap"`
	Name string `yaml:"name" mapstructure:"name" awareness:"bootstrap"`
}

// OIDCConfig configures the OIDC login flow used by the CLI and web surfaces.
type OIDCConfig struct {
	Issuer             string   `yaml:"issuer"             mapstructure:"issuer"             awareness:"required,mode:cloud" validate:"omitempty,url"`
	ClientID           string   `yaml:"clientID"           mapstructure:"clientID"           awareness:"required,mode:cloud"`
	ClientSecret       string   `yaml:"clientSecret"       mapstructure:"clientSecret"       awareness:"required,mode:cloud,secret"`
	CLIClientID        string   `yaml:"cliClientID"        mapstructure:"cliClientID"        awareness:"-"`
	Scopes             []string `yaml:"scopes"             mapstructure:"scopes"             awareness:"-"`
	RedirectPort       int      `yaml:"redirectPort"       mapstructure:"redirectPort"       awareness:"-" validate:"gte=0,lte=65535"`
	ButtonLabel        string   `yaml:"buttonLabel"        mapstructure:"buttonLabel"        awareness:"-"`
	ButtonLogoURL      string   `yaml:"buttonLogoURL"      mapstructure:"buttonLogoURL"      awareness:"-" validate:"omitempty,url"`
	EndSessionOnLogout bool     `yaml:"endSessionOnLogout" mapstructure:"endSessionOnLogout" awareness:"-"`
}

// APIConfig configures the Connect-RPC API surface.
type APIConfig struct {
	Enabled   bool          `yaml:"enabled"   mapstructure:"enabled"`
	KeyPrefix string        `yaml:"keyPrefix" mapstructure:"keyPrefix" awareness:"-"`
	OpenAPI   OpenAPIConfig `yaml:"openapi"   mapstructure:"openapi"`
}

// MCPConfig configures the Model Context Protocol surface mounted at basePath+"/mcp".
type MCPConfig struct {
	Enabled          bool   `yaml:"enabled"          mapstructure:"enabled"          awareness:"-"`
	Audience         string `yaml:"audience"         mapstructure:"audience"         awareness:"-"`
	AudienceOverride bool   `yaml:"audienceOverride" mapstructure:"audienceOverride" awareness:"-"`
	AppsUI           bool   `yaml:"appsUI"           mapstructure:"appsUI"           awareness:"-"`
	// Introspection registers the MCP resource server at the issuer's RFC 7662 endpoint, for personal tokens.
	Introspection tokens.IntrospectionConfig `yaml:"introspection" mapstructure:"introspection" awareness:"-"`
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

// SchedulerConfig tunes the periodic-job runner.
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

// QueueConfig configures the NATS JetStream connection behind queue.Submit and the consumer.
type QueueConfig struct {
	Enabled        bool          `yaml:"enabled"        mapstructure:"enabled"        awareness:"-"`
	URL            string        `yaml:"url"            mapstructure:"url"            awareness:"secret"`
	Token          string        `yaml:"token"          mapstructure:"token"          awareness:"secret"`
	User           string        `yaml:"user"           mapstructure:"user"           awareness:"-"`
	Password       string        `yaml:"password"       mapstructure:"password"       awareness:"secret"`
	ConnectTimeout time.Duration `yaml:"connectTimeout" mapstructure:"connectTimeout" awareness:"-"      validate:"gte=0"`
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

func validateInvariants(c *Config) error {
	if err := validateGenesisPasswordNeedsEmail(c); err != nil {
		return err
	}
	if err := validateTokens(c); err != nil {
		return err
	}
	if err := validateMCP(c); err != nil {
		return err
	}
	if err := validateQueueNeedsURL(c); err != nil {
		return err
	}
	if err := validateQueueCredentials(c); err != nil {
		return err
	}
	if err := validateOpensheet(c); err != nil {
		return err
	}
	switch c.Mode {
	case ModeSelfhosted:
		if err := validateSelfhosted(c); err != nil {
			return err
		}
	case ModeCloud:
		if err := validateCloud(c); err != nil {
			return err
		}
	}
	return validatePostgresNeedsEncryptionKey(c)
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
	if err := validateAutoMigrateNeedsMigrator(c); err != nil {
		return err
	}
	return validateCloudEncryptionKey(c)
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

func validateQueueNeedsURL(c *Config) error {
	if c.Queue.Enabled && c.Queue.URL == "" {
		return errors.New("config: queue.enabled=true requires queue.url (set OPENSHEET_QUEUE_URL)")
	}
	return nil
}

// QueueCredentialsConflictError reports queue.token set together with queue.user or queue.password.
type QueueCredentialsConflictError struct{}

func (*QueueCredentialsConflictError) Error() string {
	return "config: queue.token and queue.user/queue.password are mutually exclusive — a token authenticates to a server without accounts, a user to one account (unset OPENSHEET_QUEUE_TOKEN, or unset OPENSHEET_QUEUE_USER and OPENSHEET_QUEUE_PASSWORD)"
}

// IsQueueCredentialsConflictError reports whether err is a *QueueCredentialsConflictError.
func IsQueueCredentialsConflictError(err error) bool {
	var target *QueueCredentialsConflictError
	return errors.As(err, &target)
}

// QueueCredentialsIncompleteError reports queue.user without queue.password, or the reverse.
type QueueCredentialsIncompleteError struct {
	Missing string
}

func (e *QueueCredentialsIncompleteError) Error() string {
	return fmt.Sprintf("config: queue.user and queue.password are set together — %s is missing (set OPENSHEET_QUEUE_%s)", e.Missing, strings.ToUpper(strings.TrimPrefix(e.Missing, "queue.")))
}

// IsQueueCredentialsIncompleteError reports whether err is a *QueueCredentialsIncompleteError.
func IsQueueCredentialsIncompleteError(err error) bool {
	var target *QueueCredentialsIncompleteError
	return errors.As(err, &target)
}

func validateQueueCredentials(c *Config) error {
	q := c.Queue
	if q.Token != "" && (q.User != "" || q.Password != "") {
		return &QueueCredentialsConflictError{}
	}
	if q.User != "" && q.Password == "" {
		return &QueueCredentialsIncompleteError{Missing: "queue.password"}
	}
	if q.Password != "" && q.User == "" {
		return &QueueCredentialsIncompleteError{Missing: "queue.user"}
	}
	return nil
}

func validatePostgresNeedsEncryptionKey(c *Config) error {
	if c.DB.Driver == db.DriverPostgres && c.Security.EncryptionKey == "" {
		return errors.New("config: db.driver=postgres requires security.encryptionKey — 32 bytes hex or base64; without it persisted sessions cannot be sealed and every login fails (set OPENSHEET_SECURITY_ENCRYPTION_KEY)")
	}
	return nil
}

func validateCloudEncryptionKey(c *Config) error {
	if c.Security.EncryptionKey == "" {
		return errors.New("config: mode=cloud requires security.encryptionKey — 32 bytes hex or base64; without it persisted sessions cannot be sealed and every login fails (set OPENSHEET_SECURITY_ENCRYPTION_KEY)")
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
	if c.Tenant.SingletonOrg.Name == "" {
		return errors.New("config: mode=cloud requires tenant.singletonOrg.name — the display name of the first organization (set OPENSHEET_TENANT_SINGLETON_ORG_NAME)")
	}
	return nil
}

func validateAutoMigrateNeedsMigrator(c *Config) error {
	if !c.DB.AllowBypassRLS && c.DB.AutoMigrate && c.DB.Migrator.DSN == "" {
		return errors.New("config: mode=cloud with autoMigrate and RLS enforced requires db.migrator.dsn — run scripts/db/provision.sh (APP=opensheet DB_NAME=opensheet) and set OPENSHEET_DB_MIGRATOR_DSN to the opensheet_migrator credential, or disable autoMigrate and run migrations out-of-band")
	}
	return nil
}

// MCPIssuerRequiredError reports mcp.enabled with no tokens.issuer to verify MCP access tokens against.
type MCPIssuerRequiredError struct{}

func (*MCPIssuerRequiredError) Error() string {
	return "config: mcp.enabled requires tokens.issuer — no issuer, no verifier, and every MCP call would be unauthenticated (set OPENSHEET_TOKENS_ISSUER, or unset OPENSHEET_MCP_ENABLED)"
}

// IsMCPIssuerRequiredError reports whether err is an *MCPIssuerRequiredError.
func IsMCPIssuerRequiredError(err error) bool {
	var target *MCPIssuerRequiredError
	return errors.As(err, &target)
}

// MCPBaseURLRequiredError reports mcp.enabled with neither http.baseURL nor an explicit mcp.audience.
type MCPBaseURLRequiredError struct{}

func (*MCPBaseURLRequiredError) Error() string {
	return "config: mcp.enabled requires http.baseURL to derive mcp.audience — without it the audience cannot name this deployment (set OPENSHEET_HTTP_BASE_URL, or set OPENSHEET_MCP_AUDIENCE with OPENSHEET_MCP_AUDIENCE_OVERRIDE=true)"
}

// IsMCPBaseURLRequiredError reports whether err is an *MCPBaseURLRequiredError.
func IsMCPBaseURLRequiredError(err error) bool {
	var target *MCPBaseURLRequiredError
	return errors.As(err, &target)
}

// MCPAudienceInvalidError reports an mcp.audience that is not an absolute, fragment-free URL.
type MCPAudienceInvalidError struct {
	Audience string
	Reason   string
}

func (e *MCPAudienceInvalidError) Error() string {
	return fmt.Sprintf("config: mcp.audience %q is %s — a token audience must be an absolute, fragment-free URL identifying this MCP endpoint (set OPENSHEET_MCP_AUDIENCE)", e.Audience, e.Reason)
}

// IsMCPAudienceInvalidError reports whether err is an *MCPAudienceInvalidError.
func IsMCPAudienceInvalidError(err error) bool {
	var target *MCPAudienceInvalidError
	return errors.As(err, &target)
}

// MCPAudienceMismatchError reports an mcp.audience that names something other than the mounted endpoint.
type MCPAudienceMismatchError struct {
	Audience string
	Mounted  string
}

func (e *MCPAudienceMismatchError) Error() string {
	// SECURITY: an audience naming another resource means tokens minted for that resource are accepted here.
	return fmt.Sprintf("config: mcp.audience %q does not match the mounted endpoint %q — tokens minted for another resource would be accepted (set OPENSHEET_MCP_AUDIENCE=%s, or OPENSHEET_MCP_AUDIENCE_OVERRIDE=true when a proxy rewrites the public URL)", e.Audience, e.Mounted, e.Mounted)
}

// IsMCPAudienceMismatchError reports whether err is an *MCPAudienceMismatchError.
func IsMCPAudienceMismatchError(err error) bool {
	var target *MCPAudienceMismatchError
	return errors.As(err, &target)
}

// MCPAudienceCollisionError reports an mcp.audience equal to tokens.audience.
type MCPAudienceCollisionError struct {
	Audience string
}

func (e *MCPAudienceCollisionError) Error() string {
	// SECURITY: a shared audience lets a token minted for the MCP endpoint, carrying only its narrow scopes, authenticate on the control plane.
	return fmt.Sprintf("config: mcp.audience and tokens.audience are both %q — the MCP endpoint and the control plane must have distinct token audiences (set OPENSHEET_MCP_AUDIENCE or OPENSHEET_TOKENS_AUDIENCE)", e.Audience)
}

// IsMCPAudienceCollisionError reports whether err is an *MCPAudienceCollisionError.
func IsMCPAudienceCollisionError(err error) bool {
	var target *MCPAudienceCollisionError
	return errors.As(err, &target)
}

func validateMCP(c *Config) error {
	if !c.MCP.Enabled {
		return nil
	}
	if err := validateMCPNeedsTokensIssuer(c); err != nil {
		return err
	}
	if err := validateMCPNeedsBaseURL(c); err != nil {
		return err
	}
	return validateMCPAudience(c)
}

func validateMCPNeedsTokensIssuer(c *Config) error {
	if c.Tokens.Issuer == "" {
		return &MCPIssuerRequiredError{}
	}
	return nil
}

func validateMCPNeedsBaseURL(c *Config) error {
	if c.MCP.Audience == "" && c.HTTP.BaseURL == "" {
		return &MCPBaseURLRequiredError{}
	}
	return nil
}

func validateMCPAudience(c *Config) error {
	mounted := defaultMCPAudience(c.HTTP.BaseURL, c.HTTP.BasePath)
	if c.MCP.Audience == "" {
		c.MCP.Audience = mounted
	}
	u, err := url.Parse(c.MCP.Audience)
	if err != nil {
		return &MCPAudienceInvalidError{Audience: c.MCP.Audience, Reason: "not a URL"}
	}
	if !u.IsAbs() {
		return &MCPAudienceInvalidError{Audience: c.MCP.Audience, Reason: "not absolute"}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &MCPAudienceInvalidError{Audience: c.MCP.Audience, Reason: "not an http(s) URL"}
	}
	if u.Host == "" {
		return &MCPAudienceInvalidError{Audience: c.MCP.Audience, Reason: "missing a host"}
	}
	if strings.Contains(c.MCP.Audience, "#") {
		return &MCPAudienceInvalidError{Audience: c.MCP.Audience, Reason: "carrying a fragment"}
	}
	if !c.MCP.AudienceOverride && c.MCP.Audience != mounted {
		return &MCPAudienceMismatchError{Audience: c.MCP.Audience, Mounted: mounted}
	}
	if c.Tokens.Audience == c.MCP.Audience {
		return &MCPAudienceCollisionError{Audience: c.MCP.Audience}
	}
	return nil
}

// TokensIssuerMismatchError reports a tokens.issuer that differs from oidc.issuer, so signed-in users could never be found by their bearer tokens.
type TokensIssuerMismatchError struct {
	Tokens string
	OIDC   string
}

func (e *TokensIssuerMismatchError) Error() string {
	return fmt.Sprintf("config: tokens.issuer %q differs from oidc.issuer %q — users are keyed by (issuer, subject), so a token from another issuer finds no user (set OPENSHEET_TOKENS_ISSUER to OPENSHEET_OIDC_ISSUER)", e.Tokens, e.OIDC)
}

// IsTokensIssuerMismatchError reports whether err is a *TokensIssuerMismatchError.
func IsTokensIssuerMismatchError(err error) bool {
	_, ok := errors.AsType[*TokensIssuerMismatchError](err)
	return ok
}

func validateTokens(c *Config) error {
	if c.Tokens.Issuer != "" && c.OIDC.Issuer != "" && c.Tokens.Issuer != c.OIDC.Issuer {
		return &TokensIssuerMismatchError{Tokens: c.Tokens.Issuer, OIDC: c.OIDC.Issuer}
	}
	if c.Tokens.Audience == "" && c.HTTP.BaseURL != "" {
		c.Tokens.Audience = defaultAPIAudience(c.HTTP.BaseURL, c.HTTP.BasePath)
	}
	return nil
}

func defaultAPIAudience(baseURL, basePath string) string {
	return strings.TrimRight(baseURL, "/") + strings.TrimRight(basePath, "/") + "/api"
}

func defaultMCPAudience(baseURL, basePath string) string {
	return strings.TrimRight(baseURL, "/") + strings.TrimRight(basePath, "/") + "/mcp"
}
