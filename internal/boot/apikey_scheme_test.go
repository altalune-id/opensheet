package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPIKeyPrefix_HasOneReaderInBoot proves no surface gate can drift from the scheme that mints and resolves keys.
func TestAPIKeyPrefix_HasOneReaderInBoot(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	readers := map[string]int{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name)
		require.NoError(t, readErr)
		if n := strings.Count(string(src), "cfg.API.KeyPrefix"); n > 0 {
			readers[name] = n
		}
	}

	require.Equal(t, map[string]int{"services.go": 1}, readers,
		"cfg.API.KeyPrefix must be read once, into apikey.NewScheme; every gate derives from KeyAuthn.Scheme()")

	services, err := os.ReadFile("services.go")
	require.NoError(t, err)
	require.Contains(t, string(services), "keyScheme := apikey.NewScheme(cfg.API.KeyPrefix)")
	require.Contains(t, string(services), "apikey.NewAuthenticator(keyStore, keyUsage, keyScheme)")
	require.Contains(t, string(services), "apikey.NewService(keyStore, keyScheme,")
}
