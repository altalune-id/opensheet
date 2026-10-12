package handlers

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/platform/events"
)

// TestWebhookEventLabels_TranslatedInEveryLocale keeps the next catalog entry from rendering as a raw key in any locale.
func TestWebhookEventLabels_TranslatedInEveryLocale(t *testing.T) {
	t.Parallel()
	fsys := i18n.EmbeddedLocalesFS()
	files, err := fs.Glob(fsys, "locales/"+i18n.LocaleFilenamePrefix+"*"+i18n.LocaleFilenameSuffix)
	require.NoError(t, err)
	require.Len(t, files, 5)
	for _, f := range files {
		buf, err := fs.ReadFile(fsys, f)
		require.NoError(t, err)
		var msgs map[string]any
		require.NoError(t, yaml.Unmarshal(buf, &msgs), f)
		for _, s := range events.All() {
			key := eventLabelKey(s.Type)
			require.True(t, strings.HasPrefix(key, "webhooks.event."), key)
			v, ok := msgs[key].(string)
			assert.True(t, ok && strings.TrimSpace(v) != "", "%s: %s is missing or empty", f, key)
		}
	}
}
