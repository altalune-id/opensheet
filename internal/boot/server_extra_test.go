package boot

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/config"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestResolveStateSecret_ExplicitBase64URL(t *testing.T) {
	t.Parallel()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	cfg := &config.Config{}
	cfg.HTTP.StateSecret = base64.RawURLEncoding.EncodeToString(raw)

	got, err := resolveStateSecret(cfg, discardLogger())
	require.NoError(t, err)
	assert.Equal(t, raw, got)
}

func TestResolveStateSecret_ExplicitBase64Std(t *testing.T) {
	t.Parallel()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	cfg := &config.Config{}
	cfg.HTTP.StateSecret = base64.StdEncoding.EncodeToString(raw)

	got, err := resolveStateSecret(cfg, discardLogger())
	require.NoError(t, err)
	assert.Equal(t, raw, got)
}

func TestResolveStateSecret_TooShortErrors(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.HTTP.StateSecret = base64.RawURLEncoding.EncodeToString([]byte("short"))
	_, err := resolveStateSecret(cfg, discardLogger())
	require.Error(t, err)
}

func TestResolveStateSecret_InvalidBase64Errors(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.HTTP.StateSecret = "not-base64!!$"
	_, err := resolveStateSecret(cfg, discardLogger())
	require.Error(t, err)
}

func TestResolveStateSecret_EmptyReturnsEphemeral(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	got, err := resolveStateSecret(cfg, discardLogger())
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.GreaterOrEqual(t, len(got), 32)
}

func TestBootClient_Wires(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.HTTP.BaseURL = "http://127.0.0.1:0"
	c, err := BootClient(context.Background(), cfg, "sometoken")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.NotNil(t, c.Log)
	assert.NotNil(t, c.Conn)
	assert.NotNil(t, c.Reporter)
	assert.Equal(t, cfg, c.Cfg)
}

func TestBuildAltAuth_NilWhenOIDCUnset(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.OIDC.Issuer = ""
	got, err := buildAltAuth(context.Background(), cfg, discardLogger())
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestOIDCRedirectURL pins the /oauth/callback path so a typo can't silently break OIDC login.
func TestOIDCRedirectURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		base    string
		want    string
	}{
		{"empty base URL returns empty", "", "", ""},
		{"root mount", "http://127.0.0.1:5150", "", "http://127.0.0.1:5150/oauth/callback"},
		{"trailing slash trimmed", "http://127.0.0.1:5150/", "", "http://127.0.0.1:5150/oauth/callback"},
		{"basePath preserved", "https://app.example.com", "/altempl", "https://app.example.com/altempl/oauth/callback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{}
			cfg.HTTP.BaseURL = tc.baseURL
			cfg.HTTP.BasePath = tc.base
			assert.Equal(t, tc.want, oidcRedirectURL(cfg))
		})
	}
}

func TestOnboardPolicyFrom_ModeSelfhosted(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Mode = config.ModeSelfhosted
	pol := onboardPolicyFrom(cfg)
	assert.NotEmpty(t, pol.Mode)
}

func TestOnboardPolicyFrom_ModeCloud(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Mode = config.ModeCloud
	pol := onboardPolicyFrom(cfg)
	assert.NotEmpty(t, pol.Mode)
}
