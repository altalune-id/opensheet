package tokens

import "time"

// Config configures the bearer-token Verifier of one resource server at the Othra issuer.
type Config struct {
	Issuer        string              `mapstructure:"issuer"        yaml:"issuer"        awareness:"required,mode:cloud"`
	Audience      string              `mapstructure:"audience"      yaml:"audience"      awareness:"required,mode:cloud,bootstrap"`
	ClockSkew     time.Duration       `mapstructure:"clockSkew"     yaml:"clockSkew"     awareness:"-"`
	Introspection IntrospectionConfig `mapstructure:"introspection" yaml:"introspection" awareness:"-"`
}

// IntrospectionConfig is the resource server's registration at the issuer's RFC 7662 endpoint; an empty ClientID turns personal tokens off.
type IntrospectionConfig struct {
	ClientID     string `mapstructure:"clientID"     yaml:"clientID"     awareness:"-"`
	ClientSecret string `mapstructure:"clientSecret" yaml:"clientSecret" awareness:"secret"`
	MaxPerMinute int    `mapstructure:"maxPerMinute" yaml:"maxPerMinute" awareness:"-"      validate:"gte=0"`
}

// Enabled reports whether personal tokens are accepted through introspection.
func (c IntrospectionConfig) Enabled() bool { return c.ClientID != "" }
