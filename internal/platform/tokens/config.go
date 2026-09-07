package tokens

import "time"

// Config configures the OIDC-backed bearer-token Verifier.
type Config struct {
	Issuer        string        `mapstructure:"issuer"        yaml:"issuer"        awareness:"required,mode:cloud"`
	JWKSURL       string        `mapstructure:"jwksURL"       yaml:"jwksURL"       awareness:"required,mode:cloud"`
	Audience      string        `mapstructure:"audience"      yaml:"audience"      awareness:"required,mode:cloud,bootstrap"`
	JWKSCacheTTL  time.Duration `mapstructure:"jwksCacheTTL"  yaml:"jwksCacheTTL"`
	ClockSkew     time.Duration `mapstructure:"clockSkew"     yaml:"clockSkew"`
	SupportedAlgs []string      `mapstructure:"supportedAlgs" yaml:"supportedAlgs"`
	AcceptRS256   bool          `mapstructure:"acceptRS256"   yaml:"acceptRS256"`
}
