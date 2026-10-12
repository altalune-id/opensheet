package config

import (
	"slices"
	"strings"
	"testing"
)

func mcpTestConfig() *Config {
	c := &Config{
		Mode:     ModeSelfhosted,
		DB:       validDB(),
		Genesis:  GenesisConfig{Email: "root@example.com", Password: "x"},
		Security: validSecurity(),
	}
	c.Tenant.SingletonOrg.Slug = "default"
	c.Tenant.SingletonOrg.Name = "Default Organization"
	return c
}

func validMCPConfig() *Config {
	c := mcpTestConfig()
	c.HTTP.BaseURL = "https://app.example.com"
	c.Tokens.Issuer = "https://issuer.example.com"
	c.MCP.Enabled = true
	return c
}

func TestValidate_MCPInvariants(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Config)
		is       func(error) bool
		wantSubs []string
		wantAud  string
	}{
		{
			name: "disabled mcp ignores every other mcp key",
			mutate: func(c *Config) {
				c.MCP.Enabled = false
				c.HTTP.BaseURL = ""
				c.Tokens.Issuer = ""
				c.MCP.Audience = "not a url#frag"
				c.MCP.AppsUI = true
			},
			wantAud: "not a url#frag",
		},
		{
			name:     "enabled without tokens.issuer is refused",
			mutate:   func(c *Config) { c.Tokens.Issuer = "" },
			is:       IsMCPIssuerRequiredError,
			wantSubs: []string{"mcp.enabled", "tokens.issuer", "OPENSHEET_TOKENS_ISSUER"},
		},
		{
			name:     "enabled without http.baseURL and no explicit audience is refused",
			mutate:   func(c *Config) { c.HTTP.BaseURL = "" },
			is:       IsMCPBaseURLRequiredError,
			wantSubs: []string{"http.baseURL", "OPENSHEET_HTTP_BASE_URL", "OPENSHEET_MCP_AUDIENCE"},
		},
		{
			name:    "empty audience defaults to the mounted endpoint",
			mutate:  func(*Config) {},
			wantAud: "https://app.example.com/mcp",
		},
		{
			name:    "empty audience honours http.basePath",
			mutate:  func(c *Config) { c.HTTP.BasePath = "/app" },
			wantAud: "https://app.example.com/app/mcp",
		},
		{
			name:    "empty audience tolerates a trailing slash on http.baseURL",
			mutate:  func(c *Config) { c.HTTP.BaseURL = "https://app.example.com/" },
			wantAud: "https://app.example.com/mcp",
		},
		{
			name:    "an explicit audience equal to the mount is accepted",
			mutate:  func(c *Config) { c.MCP.Audience = "https://app.example.com/mcp" },
			wantAud: "https://app.example.com/mcp",
		},
		{
			name:     "a mismatched audience without the override is refused",
			mutate:   func(c *Config) { c.MCP.Audience = "https://someone-elses-proxy.example.com/mcp" },
			is:       IsMCPAudienceMismatchError,
			wantSubs: []string{"someone-elses-proxy", "https://app.example.com/mcp", "OPENSHEET_MCP_AUDIENCE_OVERRIDE"},
		},
		{
			name: "a mismatched audience with the override is accepted",
			mutate: func(c *Config) {
				c.MCP.Audience = "https://proxy.example.com/mcp"
				c.MCP.AudienceOverride = true
			},
			wantAud: "https://proxy.example.com/mcp",
		},
		{
			name: "the override still requires an absolute audience",
			mutate: func(c *Config) {
				c.MCP.Audience = "/relative/path"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"/relative/path", "not absolute", "OPENSHEET_MCP_AUDIENCE"},
		},
		{
			name: "a scheme-relative audience is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "//proxy.example.com/mcp"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"not absolute"},
		},
		{
			name: "a non-http scheme is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "urn:example:mcp"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"urn:example:mcp", "http(s)"},
		},
		{
			name: "an ftp audience is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "ftp://h/mcp"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"ftp://h/mcp", "http(s)"},
		},
		{
			name: "an audience without a host is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "https:///mcp"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"https:///mcp", "host"},
		},
		{
			name: "an http audience with a port is accepted under the override",
			mutate: func(c *Config) {
				c.MCP.Audience = "http://localhost:5150/mcp"
				c.MCP.AudienceOverride = true
			},
			wantAud: "http://localhost:5150/mcp",
		},
		{
			name: "an https audience on another host is accepted under the override",
			mutate: func(c *Config) {
				c.MCP.Audience = "https://app.example/mcp"
				c.MCP.AudienceOverride = true
			},
			wantAud: "https://app.example/mcp",
		},
		{
			name:     "a tokens.audience equal to the mounted mcp audience is refused",
			mutate:   func(c *Config) { c.Tokens.Audience = "https://app.example.com/mcp" },
			is:       IsMCPAudienceCollisionError,
			wantSubs: []string{"https://app.example.com/mcp", "tokens.audience", "OPENSHEET_TOKENS_AUDIENCE"},
		},
		{
			name: "a tokens.audience equal to an overridden mcp audience is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "https://proxy.example.com/mcp"
				c.MCP.AudienceOverride = true
				c.Tokens.Audience = "https://proxy.example.com/mcp"
			},
			is:       IsMCPAudienceCollisionError,
			wantSubs: []string{"https://proxy.example.com/mcp", "tokens.audience"},
		},
		{
			name:    "a tokens.audience distinct from the mcp audience is accepted",
			mutate:  func(c *Config) { c.Tokens.Audience = "https://app.example.com/api" },
			wantAud: "https://app.example.com/mcp",
		},
		{
			name: "a fragment in the audience is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "https://proxy.example.com/mcp#fragment"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"fragment"},
		},
		{
			name: "an unparseable audience is refused",
			mutate: func(c *Config) {
				c.MCP.Audience = "https://[::1/mcp"
				c.MCP.AudienceOverride = true
			},
			is:       IsMCPAudienceInvalidError,
			wantSubs: []string{"not a URL"},
		},
		{
			name: "an absolute audience with the override needs no http.baseURL",
			mutate: func(c *Config) {
				c.HTTP.BaseURL = ""
				c.MCP.Audience = "https://proxy.example.com/mcp"
				c.MCP.AudienceOverride = true
			},
			wantAud: "https://proxy.example.com/mcp",
		},
		{
			name: "the override does not skip the derived default",
			mutate: func(c *Config) {
				c.MCP.AudienceOverride = true
			},
			wantAud: "https://app.example.com/mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validMCPConfig()
			tt.mutate(cfg)

			err := cfg.Validate()
			if tt.is == nil {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				if cfg.MCP.Audience != tt.wantAud {
					t.Fatalf("MCP.Audience = %q, want %q", cfg.MCP.Audience, tt.wantAud)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: want an error, got nil")
			}
			if !tt.is(err) {
				t.Fatalf("Validate: wrong error type: %v", err)
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("Validate error %q does not mention %q", err, sub)
				}
			}
		})
	}
}

func TestValidate_MCPErrorHelpersRejectOtherErrors(t *testing.T) {
	cfg := validMCPConfig()
	cfg.Tokens.Issuer = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate: want an error, got nil")
	}
	for name, is := range map[string]func(error) bool{
		"IsMCPBaseURLRequiredError":  IsMCPBaseURLRequiredError,
		"IsMCPAudienceInvalidError":  IsMCPAudienceInvalidError,
		"IsMCPAudienceMismatchError": IsMCPAudienceMismatchError,
	} {
		if is(err) {
			t.Fatalf("%s matched a missing-issuer error", name)
		}
	}
	if IsMCPIssuerRequiredError(nil) {
		t.Fatal("IsMCPIssuerRequiredError(nil) must be false")
	}
}

func TestLoad_MCPKeysBindFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("OPENSHEET_HTTP_BASE_URL", "https://app.example.com")
	t.Setenv("OPENSHEET_TOKENS_ISSUER", "https://issuer.example.com")
	t.Setenv("OPENSHEET_MCP_ENABLED", "true")
	t.Setenv("OPENSHEET_MCP_APPS_UI", "true")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MCP.Enabled {
		t.Fatal("OPENSHEET_MCP_ENABLED=true did not reach Config.MCP.Enabled")
	}
	if !cfg.MCP.AppsUI {
		t.Fatal("OPENSHEET_MCP_APPS_UI=true did not reach Config.MCP.AppsUI")
	}
	if cfg.MCP.Audience != "https://app.example.com/mcp" {
		t.Fatalf("MCP.Audience = %q, want the derived mount", cfg.MCP.Audience)
	}
}

func TestLoad_MCPDefaultsOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCP != (MCPConfig{}) {
		t.Fatalf("MCP defaults must be the zero value so a literal-built Config matches the loaded one, got %+v", cfg.MCP)
	}
}

func TestEnvKeys_MCPKeysAreRuntime(t *testing.T) {
	want := map[string][]string{
		"OPENSHEET_MCP_ENABLED":           nil,
		"OPENSHEET_MCP_AUDIENCE":          nil,
		"OPENSHEET_MCP_AUDIENCE_OVERRIDE": nil,
		"OPENSHEET_MCP_APPS_UI":           nil,
		"OPENSHEET_MCP_CHALLENGE_TOKEN":   nil,
		"OPENSHEET_MCP_CHALLENGE_PREFIX":  nil,
	}
	seen := map[string]bool{}
	for _, k := range WalkEnvKeys("OPENSHEET") {
		if !strings.HasPrefix(k.YAML, "mcp.") {
			continue
		}
		awareness, ok := want[k.Key]
		if !ok {
			t.Fatalf("unexpected mcp env key %q", k.Key)
		}
		seen[k.Key] = true
		if !slices.Equal(k.Awareness, awareness) {
			t.Fatalf("%s: awareness = %v, want %v", k.Key, k.Awareness, awareness)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Fatalf("%s is not a known env key", key)
		}
	}
}
