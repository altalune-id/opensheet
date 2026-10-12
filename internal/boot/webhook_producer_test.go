package boot_test

import (
	"testing"

	"altalune.id/opensheet/internal/platform/config"
)

func TestBootServer_PublishEnqueuesAWebhookDelivery(t *testing.T) {
	assertPublishEnqueues(t, newSmokeCfg(t))
}

// TODO: restore once an opensheet module emits a subscribable event; the blog publish this drove is gone.
func assertPublishEnqueues(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg == nil {
		t.Fatal("assertPublishEnqueues needs a config")
	}
	t.Skip("opensheet: no module emits a subscribable webhook event since blog was removed")
}
