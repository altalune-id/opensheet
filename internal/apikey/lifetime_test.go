package apikey_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func in(d time.Duration) *time.Time {
	t := time.Now().UTC().Add(d)
	return &t
}

// SECURITY: every new key expires within a year, so a leaked or forgotten key dies on its own; keys already in the field are untouched.
func TestEveryMintRequiresABoundedLifetime(t *testing.T) {
	f := newOrgKeyFixture(t)
	orgCtx := tenant.Into(t.Context(), f.admin)
	projCtx := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.a, UserID: f.admin.UserID})
	scopes := []string{authn.ScopeSheetsRead}
	all := apikey.ProjectGrant{All: true}

	mints := map[string]func(*time.Time) error{
		"project": func(exp *time.Time) error { _, _, err := f.svc.Mint(projCtx, "k", scopes, nil, exp); return err },
		"org":     func(exp *time.Time) error { _, _, err := f.svc.MintOrg(orgCtx, "k", scopes, all, exp); return err },
		"personal": func(exp *time.Time) error {
			_, _, err := f.svc.MintPersonal(orgCtx, "k", scopes, all, exp)
			return err
		},
	}
	for name, mint := range mints {
		t.Run(name, func(t *testing.T) {
			assert.True(t, apikey.IsExpiryRequiredError(mint(nil)), "no expiry")
			assert.True(t, apikey.IsExpiryInPastError(mint(in(-time.Minute))), "past expiry")
			assert.True(t, apikey.IsExpiryTooLongError(mint(in(apikey.MaxLifetime+time.Hour))), "beyond a year")
			require.NoError(t, mint(in(apikey.MaxLifetime-time.Minute)), "just under a year")
		})
	}
}

func TestAKeyInTheFieldWithNoExpiryStillAuthenticates(t *testing.T) {
	store := fakes.NewAPIKey()
	k, plaintext, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "legacy", []string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
	require.NoError(t, err)
	store.Seed(k)
	_, err = apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()).Authenticate(t.Context(), plaintext)
	require.NoError(t, err, "a never-expiring key minted before the rule must keep working")
}
