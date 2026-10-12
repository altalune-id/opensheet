package handlers_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"altalune.id/opensheet/internal/platform/authn"
)

// TestEveryMintableScopeHasALabelInEveryLocale: the mint form renders a missing label as its raw key, and the apikey.scope.* claim hides it from i18n-check.
func TestEveryMintableScopeHasALabelInEveryLocale(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "i18n", "locales", "active.*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var keys map[string]any
		require.NoError(t, yaml.Unmarshal(raw, &keys), f)
		for _, scope := range authn.MintableScopes() {
			key := "apikey.scope." + strings.ReplaceAll(scope, ":", "_")
			_, ok := keys[key]
			require.True(t, ok, "%s has no label %s for scope %s", filepath.Base(f), key, scope)
		}
	}
}
