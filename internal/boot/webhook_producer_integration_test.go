//go:build integration

package boot_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/testutil/pgtest"
)

func TestPostgres_BootServer_PublishEnqueuesAWebhookDelivery(t *testing.T) {
	h := pgtest.New(t)
	_ = h.OpenDB(t)
	u, err := url.Parse(h.DSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", h.Schema)
	u.RawQuery = q.Encode()

	cfg := newSmokeCfg(t)
	cfg.DB.Driver = db.DriverPostgres
	cfg.DB.DSN = u.String()
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	assertPublishEnqueues(t, cfg)
}
