package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvKeys_GenesisEmailIsNoLongerBootstrap(t *testing.T) {
	keys := WalkEnvKeys("ALT")
	var found bool
	for _, k := range keys {
		if k.YAML != "genesis.email" {
			continue
		}
		found = true
		require.NotContains(t, k.Awareness, "bootstrap",
			"genesis.email is reconciled every boot; the inherited bootstrap marker must be opted out with awareness:\"-\"")
	}
	require.True(t, found, "genesis.email must still be a known env key")
}

func TestEnvKeys_OnboardSetupTokenIsSecret(t *testing.T) {
	keys := WalkEnvKeys("ALT")
	var found bool
	for _, k := range keys {
		if k.YAML != "onboard.setupToken" {
			continue
		}
		found = true
		require.Contains(t, k.Awareness, "secret")
		require.NotContains(t, k.Awareness, "bootstrap")
	}
	require.True(t, found, "onboard.setupToken must be a known env key")
}

func TestLoad_OnboardSetupTokenFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALT_ONBOARD_SETUP_TOKEN", "pinned-token")
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "pinned-token", cfg.Onboard.SetupToken)
}
