package boot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	webhandlers "altalune.id/opensheet/internal/web/handlers"
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

// TestResolveStateSecret_ExplicitHex pins the one documented format story: the same 64-hex value keys both security.encryptionKey and http.stateSecret.
func TestResolveStateSecret_ExplicitHex(t *testing.T) {
	t.Parallel()
	raw := bytes.Repeat([]byte{0x3C}, 32)
	cfg := &config.Config{}
	cfg.HTTP.StateSecret = hex.EncodeToString(raw)

	got, err := resolveStateSecret(cfg, discardLogger())
	require.NoError(t, err)
	assert.Equal(t, raw, got)

	sealed, err := sealer.ParseKey(cfg.HTTP.StateSecret)
	require.NoError(t, err, "a hex state secret must also parse as a sealer key")
	assert.Equal(t, got, sealed, "both secrets must decode one hex value to the same bytes")
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

// TestNewWebDeps_CarriesTheResolvedStateSecret guards the double-keying regression: web Deps must hold the bytes resolveStateSecret produced, not a second derivation from cfg.HTTP.StateSecret.
func TestNewWebDeps_CarriesTheResolvedStateSecret(t *testing.T) {
	t.Parallel()
	raw := bytes.Repeat([]byte{0x5A}, 32)
	cfg := &config.Config{}
	cfg.HTTP.StateSecret = base64.RawURLEncoding.EncodeToString(raw)

	secret, err := resolveStateSecret(cfg, discardLogger())
	require.NoError(t, err)
	require.Equal(t, raw, secret)

	deps, err := newWebDeps(cfg, capabilities.Capabilities{}, session.NewMemoryStore(), discardLogger(), secret)
	require.NoError(t, err)
	assert.Equal(t, secret, deps.SecretBytes())
	assert.NotEqual(t, []byte(cfg.HTTP.StateSecret), deps.SecretBytes(),
		"web Deps must not re-key off the raw config string")
}

// TestNewWebDeps_RejectsAShortSecret proves boot refuses to build web Deps on a key too short to sign a cookie, instead of relocating the silent-empty-key bug into the handlers.
func TestNewWebDeps_RejectsAShortSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secret []byte
	}{
		{"nil", nil},
		{"one byte short", bytes.Repeat([]byte{0x01}, webhandlers.MinSecretLen-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newWebDeps(&config.Config{}, capabilities.Capabilities{},
				session.NewMemoryStore(), discardLogger(), tt.secret)
			require.Error(t, err)
			assert.True(t, webhandlers.IsShortSecretError(err), "want a *ShortSecretError, got %T", err)
		})
	}
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
	got, err := buildAltAuth(context.Background(), cfg, bytes.Repeat([]byte{0x01}, 32))
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
		{"basePath preserved", "https://app.example.com", "/opensheet", "https://app.example.com/opensheet/oauth/callback"},
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
