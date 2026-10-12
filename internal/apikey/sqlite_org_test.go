package apikey_test

import (
	"crypto/sha256"
	"database/sql"
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

func TestSQLiteOrgKeyRoundTrip(t *testing.T) {
	store, sqlDB, tc := newAPIKeyStoreForTest(t)
	second := seedProject(t, sqlDB, tc)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, UserID: tc.UserID})

	k, plaintext, err := apikey.Scheme{}.MintOrg(tc.OrgID, "ci", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{tc.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	k.CreatedBy = tc.UserID
	require.NoError(t, store.Save(ctx, k))

	got, err := store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Equal(t, apikey.KindOrg, got.Kind)
	assert.Equal(t, uuid.Nil, got.ProjectID)
	assert.Equal(t, []uuid.UUID{tc.ProjectID}, got.ProjectIDs)
	assert.Equal(t, k.SecretHint, got.SecretHint)
	assert.Equal(t, tc.UserID, got.CreatedBy)

	require.NoError(t, got.GrantProjects([]uuid.UUID{second}))
	require.NoError(t, store.Save(ctx, got))
	resolved, err := store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{tc.ProjectID, second}, resolved.ProjectIDs, "the hot path must carry the grant")

	require.NoError(t, resolved.GrantAllProjects())
	require.NoError(t, store.Save(ctx, resolved))
	all, err := store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.True(t, all.AllProjects)
	assert.Empty(t, all.ProjectIDs, "promoting to all projects must drop the named rows")

	orgKeys, err := store.ListOrg(ctx)
	require.NoError(t, err)
	require.Len(t, orgKeys, 1)
	projectKeys, err := store.List(ctx, tc.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, projectKeys)
}

// SECURITY: SQLite has no RLS, so the composite foreign key is what refuses a grant naming another org's project.
func TestSQLiteGrantCannotNameAnotherOrgsProject(t *testing.T) {
	store, sqlDB, tc := newAPIKeyStoreForTest(t)
	foreign := seedTenant(t, sqlDB)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, UserID: tc.UserID})

	k, _, err := apikey.Scheme{}.MintOrg(tc.OrgID, "ci", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{foreign.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.Error(t, store.Save(ctx, k), "a grant row naming another org's project must be refused")

	var n int
	require.NoError(t, sqlDB.QueryRow("SELECT count(*) FROM "+prefix+"api_key_projects").Scan(&n))
	assert.Zero(t, n)
}

func TestSQLiteKindShapeIsEnforced(t *testing.T) {
	_, sqlDB, tc := newAPIKeyStoreForTest(t)
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"api_keys (id, org_id, project_id, kind, all_projects, name, secret_hash, created_at) VALUES (?, ?, NULL, 'project', 0, 'bad', ?, ?)",
		uuid.NewString(), tc.OrgID.String(), []byte(uuid.NewString()), now)
	require.Error(t, err, "a project key without a project must violate the kind check")
}

func sha(plaintext string) [32]byte { return sha256.Sum256([]byte(plaintext)) }

func TestSQLitePersonalTokenRoundTrip(t *testing.T) {
	store, sqlDB, tc := newAPIKeyStoreForTest(t)
	other := seedTenant(t, sqlDB)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, UserID: tc.UserID})

	k, plaintext, err := apikey.Scheme{}.MintPersonal(tc.OrgID, tc.UserID, "laptop", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{All: true}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, k))

	mine, err := store.ListPersonal(ctx, tc.UserID)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, apikey.KindPersonal, mine[0].Kind)
	assert.True(t, mine[0].AllProjects)
	assert.Equal(t, tc.UserID, mine[0].CreatedBy)

	none, err := store.ListPersonal(ctx, other.UserID)
	require.NoError(t, err)
	assert.Empty(t, none, "another user's listing must not include this token")
	orgKeys, err := store.ListOrg(ctx)
	require.NoError(t, err)
	assert.Empty(t, orgKeys)

	resolved, err := store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err)
	assert.Equal(t, apikey.KindPersonal, resolved.Kind)
}

// SECURITY: SQLite has no RLS, so the org predicate is the only thing keeping one org's tokens out of another org's list and revoke.
func TestSQLitePersonalTokensStayInTheirOrg(t *testing.T) {
	store, sqlDB, a := newAPIKeyStoreForTest(t)
	b := seedTenant(t, sqlDB)
	joinOrg(t, sqlDB, b.OrgID, a.UserID)
	inA := tenant.Into(t.Context(), tenant.Context{OrgID: a.OrgID, UserID: a.UserID})
	inB := tenant.Into(t.Context(), tenant.Context{OrgID: b.OrgID, UserID: a.UserID})

	ka, _, err := apikey.Scheme{}.MintPersonal(a.OrgID, a.UserID, "a", nil, apikey.ProjectGrant{All: true}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(inA, ka))
	kb, _, err := apikey.Scheme{}.MintPersonal(b.OrgID, a.UserID, "b", nil, apikey.ProjectGrant{All: true}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(inB, kb))

	listed, err := store.ListPersonal(inA, a.UserID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, ka.ID, listed[0].ID, "org A's list must not include the same owner's token in org B")

	require.NoError(t, store.RevokePersonal(inA, a.UserID, time.Now().UTC()))
	gotA, err := store.ByID(inA, ka.ID)
	require.NoError(t, err)
	assert.NotNil(t, gotA.RevokedAt)
	gotB, err := store.ByID(inB, kb.ID)
	require.NoError(t, err)
	assert.Nil(t, gotB.RevokedAt, "leaving org A must not revoke the token in org B")
}

func joinOrg(t *testing.T, sqlDB *sql.DB, orgID, userID uuid.UUID) {
	t.Helper()
	_, err := sqlDB.Exec("INSERT INTO "+prefix+"memberships (id, org_id, user_id, role, created_at) VALUES (?, ?, ?, 'member', ?)",
		uuid.NewString(), orgID.String(), userID.String(), sqliteent.SQLiteTime(time.Now()))
	require.NoError(t, err)
}
