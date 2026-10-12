package boot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

type dataplaneFixture struct {
	srv      *boot.Server
	sheetURL string
	readKey  string
	noneKey  string
}

func newDataplaneFixture(t *testing.T) *dataplaneFixture {
	t.Helper()

	cfg := newSmokeCfg(t)
	cfg.DataPlane.Enabled = true
	cfg.Mode = config.ModeCloud
	cfg.OIDC = config.OIDCConfig{
		Issuer:       stubIssuer(t),
		ClientID:     "dp-client",
		ClientSecret: "dp-secret",
	}
	cfg.HTTP.CSP.Enabled = true

	seed, err := boot.BootServer(context.Background(), cfg)
	require.NoError(t, err)
	seedUser, err := seed.Users.Create(context.Background(), user.CreateRequest{
		Email: "dp-seed@example.com", Name: "DP Seed", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	_, err = seed.Onboards.Complete(context.Background(), seedUser.ID, onboard.MethodCLIInit)
	require.NoError(t, err)
	require.NoError(t, seed.Close())

	srv, err := boot.BootServer(context.Background(), cfg)
	require.NoError(t, err)
	require.True(t, srv.Onboarded, "the fixture must be past the onboarding gate")
	t.Cleanup(func() { _ = srv.Close() })

	ctx := context.Background()
	owner, err := srv.Users.Create(ctx, user.CreateRequest{
		Email: "dp-owner@example.com", Name: "DP Owner", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	o, err := srv.Orgs.Create(ctx, org.CreateRequest{Slug: "dp-org", Name: "DP Org", OwnerID: owner.ID})
	require.NoError(t, err)

	orgCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	p, err := srv.Projects.Create(orgCtx, o.ID, "dp-project", "DP Project")
	require.NoError(t, err)

	projCtx := tenant.WithProject(orgCtx, p.ID)
	_, readKey, err := srv.APIKeys.Mint(projCtx, "reader", []string{authn.ScopeSheetsRead}, nil, soon())
	require.NoError(t, err)
	_, noneKey, err := srv.APIKeys.Mint(projCtx, "keys-only", []string{authn.ScopeAPIKeysRead}, nil, soon())
	require.NoError(t, err)

	return &dataplaneFixture{
		srv:      srv,
		sheetURL: "/api/v1/orgs/" + o.Slug + "/projects/" + p.Slug + "/sheets/absent",
		readKey:  readKey,
		noneKey:  noneKey,
	}
}

func (f *dataplaneFixture) get(t *testing.T, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

// TestDataPlane_IsMountedAndMasksEveryMiss walks the sheet data plane on the real wiring. SECURITY: the path resolves before the caller is authorized, so every credential class sees one masked not-found for a sheet it cannot name.
func TestDataPlane_IsMountedAndMasksEveryMiss(t *testing.T) {
	f := newDataplaneFixture(t)

	keys := map[string]string{
		"no credential":   "",
		"read key":        f.readKey,
		"scope-less key":  f.noneKey,
		"unparseable key": "key_not_a_real_key",
	}
	paths := map[string]string{
		"unknown sheet":   f.sheetURL,
		"unknown org":     "/api/v1/orgs/absent/projects/dp-project/sheets/absent",
		"unknown project": "/api/v1/orgs/dp-org/projects/absent/sheets/absent",
	}
	for keyName, key := range keys {
		for pathName, path := range paths {
			t.Run(keyName+" "+pathName, func(t *testing.T) {
				rec := f.get(t, path, key)
				require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
				require.JSONEq(t, `{"error":{"code":"`+apperror.CodeSheetNotFound+`","message":"Sheet not found"}}`, rec.Body.String())
			})
		}
	}
}

// TestDataPlane_RunsOnTheDataChainNotTheConsoleChain is the mount guard: no CSP header or session cookie on /api/v1.
func TestDataPlane_RunsOnTheDataChainNotTheConsoleChain(t *testing.T) {
	f := newDataplaneFixture(t)

	console := f.get(t, "/login", "")
	require.NotEmpty(t, console.Header().Get("Content-Security-Policy"),
		"the console chain must set CSP, or this guard proves nothing")

	for _, key := range []string{"", f.readKey} {
		rec := f.get(t, f.sheetURL, key)
		require.Empty(t, rec.Header().Get("Content-Security-Policy"),
			"the data plane ran the console chain: CSP is an SSR concern")
		require.Empty(t, rec.Header().Values("Set-Cookie"),
			"the data plane ran the console chain: it must never issue a session cookie")
		require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	}
}

func soon() *time.Time {
	t := time.Now().UTC().Add(24 * time.Hour)
	return &t
}
