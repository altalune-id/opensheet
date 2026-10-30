package config

import "testing"

func TestValidate_TokensAudienceDefaultsToTheAPIResource(t *testing.T) {
	for _, basePath := range []string{"", "/console"} {
		c := validMCPConfig()
		c.HTTP.BasePath = basePath
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if want := "https://app.example.com" + basePath + "/api"; c.Tokens.Audience != want {
			t.Fatalf("tokens.audience = %q, want %q", c.Tokens.Audience, want)
		}
		if c.Tokens.Audience == c.MCP.Audience {
			t.Fatal("the API and MCP resources must differ")
		}
	}
}

func TestValidate_ExplicitTokensAudienceIsKept(t *testing.T) {
	c := validMCPConfig()
	c.Tokens.Audience = "https://api.example.com"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Tokens.Audience != "https://api.example.com" {
		t.Fatalf("tokens.audience = %q", c.Tokens.Audience)
	}
}

func TestValidate_TokensIssuerMustMatchOIDCIssuer(t *testing.T) {
	c := validMCPConfig()
	c.OIDC.Issuer = "https://other.example.com"
	if err := c.Validate(); !IsTokensIssuerMismatchError(err) {
		t.Fatalf("err = %v, want *TokensIssuerMismatchError", err)
	}
	c.OIDC.Issuer = c.Tokens.Issuer
	if err := c.Validate(); err != nil {
		t.Fatalf("matching issuers: %v", err)
	}
}
