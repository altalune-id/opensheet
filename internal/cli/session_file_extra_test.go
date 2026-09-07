package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionFile_LoadCorruptReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not-json"), 0o600))
	_, err := loadSessionFile(path)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "no such")
}
