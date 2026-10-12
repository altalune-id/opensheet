//go:build integration

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
)

func TestPostgres_OrgKeyRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	second := seedPgProject(t, f.sqlDB, f.prefix, f.tc)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, UserID: f.tc.UserID})

	k, plaintext, err := apikey.Scheme{}.MintOrg(f.tc.OrgID, "ci", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.tc.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	k.CreatedBy = f.tc.UserID
	require.NoError(t, f.store.Save(ctx, k))

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Equal(t, apikey.KindOrg, got.Kind)
	assert.Equal(t, uuid.Nil, got.ProjectID)
	assert.Equal(t, []uuid.UUID{f.tc.ProjectID}, got.ProjectIDs)
	assert.Equal(t, k.SecretHint, got.SecretHint)
	assert.Equal(t, f.tc.UserID, got.CreatedBy)

	require.NoError(t, got.GrantProjects([]uuid.UUID{second}))
	require.NoError(t, f.store.Save(ctx, got))
	resolved, err := f.store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{f.tc.ProjectID, second}, resolved.ProjectIDs, "the definer function must carry the grant")

	require.NoError(t, resolved.GrantAllProjects())
	require.NoError(t, f.store.Save(ctx, resolved))
	all, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.True(t, all.AllProjects)
	assert.Empty(t, all.ProjectIDs)

	orgKeys, err := f.store.ListOrg(ctx)
	require.NoError(t, err)
	require.Len(t, orgKeys, 1)
	projectKeys, err := f.store.List(ctx, f.tc.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, projectKeys)
}

// SECURITY: a superuser fixture bypasses RLS, so the composite foreign key is the only guard this exercises.
func TestPostgres_GrantCannotNameAnotherOrgsProject(t *testing.T) {
	f := newPgFixture(t)
	foreign := seedPgTenant(t, f.sqlDB, f.prefix)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, UserID: f.tc.UserID})

	k, _, err := apikey.Scheme{}.MintOrg(f.tc.OrgID, "ci", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{foreign.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.Error(t, f.store.Save(ctx, k), "a grant row naming another org's project must be refused")

	var n int
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM "+f.prefix+"api_key_projects").Scan(&n))
	assert.Zero(t, n)
}

// TestPostgres_OrgKeyGrantsAreInvisibleAcrossOrgs is the RLS regression detector for api_key_projects under a NOBYPASSRLS role.
func TestPostgres_OrgKeyGrantsAreInvisibleAcrossOrgs(t *testing.T) {
	store, migDB, appConn, prefix := newPgRLSFixture(t)
	a := seedRLSTenant(t, migDB, prefix)
	b := seedRLSTenant(t, migDB, prefix)
	aCtx := tenant.Into(t.Context(), tenant.Context{OrgID: a.OrgID, UserID: a.UserID})
	bCtx := tenant.Into(t.Context(), tenant.Context{OrgID: b.OrgID, UserID: b.UserID})

	k, plaintext, err := apikey.Scheme{}.MintOrg(a.OrgID, "A only", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(aCtx, k))

	listed, err := store.ListOrg(bCtx)
	require.NoError(t, err)
	assert.Empty(t, listed, "org B must not list org A's org keys")

	tx, err := tenant.NewPgConn(appConn).BeginTenanted(t.Context(), tenant.Context{OrgID: b.OrgID})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var rows int
	require.NoError(t, tx.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+prefix+"api_key_projects WHERE key_id = $1", k.ID).Scan(&rows))
	assert.Zero(t, rows, "org A's grant rows must be invisible under org B's tenant scope")

	resolved, err := store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err, "the pre-tenant lookup must still resolve the key and its grant")
	assert.Equal(t, []uuid.UUID{a.ProjectID}, resolved.ProjectIDs)
}

func TestPostgres_PersonalTokenRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, UserID: f.tc.UserID})

	k, plaintext, err := apikey.Scheme{}.MintPersonal(f.tc.OrgID, f.tc.UserID, "laptop", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.tc.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, k))

	mine, err := f.store.ListPersonal(ctx, f.tc.UserID)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, []uuid.UUID{f.tc.ProjectID}, mine[0].ProjectIDs)
	none, err := f.store.ListPersonal(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none)

	resolved, err := f.store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err)
	assert.Equal(t, apikey.KindPersonal, resolved.Kind)
	assert.Equal(t, f.tc.UserID, resolved.CreatedBy)
}
