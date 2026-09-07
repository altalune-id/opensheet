package logger_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/logger"
)

func TestConfig_Validate_RecognisedLevels(t *testing.T) {
	t.Parallel()
	for _, lv := range []string{"", "debug", "info", "warn", "error", "INFO", "WARN"} {
		cfg := logger.Config{Level: lv}
		assert.NoError(t, cfg.Validate(), "level=%s", lv)
	}
}

func TestConfig_Validate_BadLevel(t *testing.T) {
	t.Parallel()
	cfg := logger.Config{Level: "verbose"}
	require.Error(t, cfg.Validate())
}

func TestConfig_Validate_RecognisedFormats(t *testing.T) {
	t.Parallel()
	for _, fm := range []string{"", "json", "text", "JSON", "TEXT"} {
		cfg := logger.Config{Format: fm}
		assert.NoError(t, cfg.Validate(), "format=%s", fm)
	}
}

func TestConfig_Validate_BadFormat(t *testing.T) {
	t.Parallel()
	cfg := logger.Config{Format: "xml"}
	require.Error(t, cfg.Validate())
}

func TestNew_JSONFormat(t *testing.T) {
	t.Parallel()
	lg := logger.New(logger.Config{Level: "debug", Format: "json"})
	assert.NotNil(t, lg)
}

func TestNew_DefaultFormat(t *testing.T) {
	t.Parallel()
	lg := logger.New(logger.Config{})
	assert.NotNil(t, lg)
}

func TestNew_TextFormatFallsBackToJSONWhenNotTTY(t *testing.T) {
	t.Parallel()
	lg := logger.New(logger.Config{Format: "text"})
	assert.NotNil(t, lg)
}
