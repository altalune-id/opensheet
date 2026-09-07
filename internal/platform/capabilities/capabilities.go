// Package capabilities computes a snapshot of feature flags derived from Config.
package capabilities

import "altalune.id/template/internal/platform/config"

// Capabilities is the read-only feature-flag snapshot derived from Config.
type Capabilities struct {
	Mode               config.Mode
	LocalIdentity      bool
	ExternalIdentity   bool
	OIDCButtonLabel    string
	OIDCButtonLogoURL  string
	PublicSignup       bool
	Signup             bool
	OrgCreation        bool
	InvitesEnabled     bool
	APIEnabled         bool
	TokenAuth          bool
	MailEnabled        bool
	OnboardingRequired bool
	IsProduction       bool
	BasePath           string
	BaseURL            string
}

// From derives Capabilities from a fully-loaded Config.
func From(c *config.Config) Capabilities {
	localCredentials := c.Genesis.Email != "" && c.Genesis.Password != ""
	localAllowed := c.Mode != config.ModeCloud || c.Genesis.BreakGlass
	caps := Capabilities{
		Mode:              c.Mode,
		LocalIdentity:     localCredentials && localAllowed,
		ExternalIdentity:  c.OIDC.Issuer != "",
		OIDCButtonLabel:   c.OIDC.ButtonLabel,
		OIDCButtonLogoURL: c.OIDC.ButtonLogoURL,
		APIEnabled:        c.API.Enabled,
		TokenAuth:         c.Tokens.Issuer != "",
		MailEnabled:       c.Mail.Driver != "" && (c.Mail.Driver != "console" || c.Mail.SMTP.Host != ""),
		IsProduction:      c.Mode.IsProduction(),
		BasePath:          c.HTTP.BasePath,
		BaseURL:           c.HTTP.BaseURL,
	}
	caps.PublicSignup = caps.Mode == config.ModeCloud && caps.ExternalIdentity
	caps.Signup = caps.PublicSignup
	caps.OrgCreation = caps.Mode == config.ModeCloud
	caps.InvitesEnabled = caps.Mode == config.ModeCloud || caps.ExternalIdentity
	return caps
}
