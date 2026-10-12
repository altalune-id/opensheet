package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvKeys_GenesisEmailIsNoLongerBootstrap(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")
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
	keys := WalkEnvKeys("OPENSHEET")
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
	t.Setenv("OPENSHEET_ONBOARD_SETUP_TOKEN", "pinned-token")
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "pinned-token", cfg.Onboard.SetupToken)
}

func TestEnvKeys_QueueTokenIsSecret(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")
	var found bool
	for _, k := range keys {
		if k.YAML != "queue.token" {
			continue
		}
		found = true
		require.Contains(t, k.Awareness, "secret")
	}
	require.True(t, found, "queue.token must be a known env key")
}

func TestEnvKeys_QueueURLIsSecret(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")
	var found bool
	for _, k := range keys {
		if k.YAML != "queue.url" {
			continue
		}
		found = true
		require.Contains(t, k.Awareness, "secret",
			"a NATS URL can carry user:pass@ credentials")
	}
	require.True(t, found, "queue.url must be a known env key")
}

func TestLoad_QueueTokenFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENSHEET_QUEUE_TOKEN", "pinned-nats-token")
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "pinned-nats-token", cfg.Queue.Token)
}

func TestEnvKeys_QueuePasswordIsSecret(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")
	var found bool
	for _, k := range keys {
		if k.YAML != "queue.password" {
			continue
		}
		found = true
		require.Contains(t, k.Awareness, "secret")
	}
	require.True(t, found, "queue.password must be a known env key")
}

func TestLoad_QueueUserAndPasswordFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENSHEET_QUEUE_USER", "opensheet")
	t.Setenv("OPENSHEET_QUEUE_PASSWORD", "pinned-nats-password")
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "opensheet", cfg.Queue.User)
	require.Equal(t, "pinned-nats-password", cfg.Queue.Password)
}
