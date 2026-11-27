package apikey_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type userKeys struct {
	project, org, personal *apikey.APIKey
	projectPlain           string
}

func mintKeysOf(t *testing.T, store apikey.Store, tc tenant.Context, creator uuid.UUID) userKeys {
	t.Helper()
	exp := time.Now().Add(time.Hour)
	scopes := []string{authn.ScopeSheetsRead}
	project, plain, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "ci", scopes, nil, &exp, time.Now().UTC())
	require.NoError(t, err)
	org, _, err := apikey.Scheme{}.MintOrg(tc.OrgID, "ops", scopes, apikey.ProjectGrant{All: true}, &exp, time.Now().UTC())
	require.NoError(t, err)
	personal, _, err := apikey.Scheme{}.MintPersonal(tc.OrgID, creator, "laptop", scopes, apikey.ProjectGrant{All: true}, &exp, time.Now().UTC())
	require.NoError(t, err)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, ProjectID: tc.ProjectID, UserID: creator})
	for _, k := range []*apikey.APIKey{project, org, personal} {
		k.CreatedBy = creator
		require.NoError(t, store.Save(ctx, k))
	}
	return userKeys{project: project, org: org, personal: personal, projectPlain: plain}
}

func assertRevokedOnDelete(t *testing.T, store apikey.Store, tc tenant.Context, gone, kept userKeys) {
	t.Helper()
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, ProjectID: tc.ProjectID, UserID: tc.UserID})
	for name, k := range map[string]*apikey.APIKey{"project key": gone.project, "org key": gone.org} {
		got, err := store.ByID(ctx, k.ID)
		require.NoError(t, err, "%s row survives", name)
		assert.NotNil(t, got.RevokedAt, "%s is revoked", name)
	}
	got, err := store.ByID(ctx, gone.personal.ID)
	require.NoError(t, err)
	assert.Nil(t, got.RevokedAt, "a personal token is left to its NULL owner, which refuses it already")
	for name, k := range map[string]*apikey.APIKey{"project key": kept.project, "org key": kept.org, "personal token": kept.personal} {
		got, err := store.ByID(ctx, k.ID)
		require.NoError(t, err)
		assert.Nil(t, got.RevokedAt, "another user's %s is untouched", name)
	}
	_, err = apikey.NewAuthenticator(store, nil, apikey.Scheme{}).Authenticate(t.Context(), gone.projectPlain)
	assert.True(t, authn.IsUnauthorizedError(err), "the revoked key is refused, got %v", err)
}

// SECURITY: an org or project key acts for its creator, so deleting the creator revokes it; the key row survives, so rows it authored keep their reference.
func TestDeletingAUserRevokesTheirOrgAndProjectKeys(t *testing.T) {
	store, sqlDB, tc := newAPIKeyStoreForTest(t)
	creator := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec("INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
		creator.String(), creator.String()+"@x.com", now, now)
	require.NoError(t, err)
	gone, kept := mintKeysOf(t, store, tc, creator), mintKeysOf(t, store, tc, tc.UserID)

	_, err = sqlDB.Exec("DELETE FROM "+prefix+"users WHERE id = ?", creator.String())
	require.NoError(t, err, "deleting the creator succeeds")
	assertRevokedOnDelete(t, store, tc, gone, kept)
}
