//go:build integration

package apikey_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type pgFixture struct {
	store  apikey.Store
	sqlDB  *sql.DB
	prefix string
	tc     tenant.Context
}

func newPgFixture(t *testing.T) pgFixture {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverPostgres
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true

	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	pfx := cfg.DB.TablePrefix
	tc := seedPgTenant(t, sqlDB, pfx)
	store := apikey.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: pfx},
		db.Pool{W: sqlDB, R: sqlDB},
		tenant.NewPgConn(sqlDB),
	)
	return pgFixture{store: store, sqlDB: sqlDB, prefix: pfx, tc: tc}
}

func seedPgTenant(t *testing.T, sqlDB *sql.DB, prefix string) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@example.com", now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, 'Org', $3, $4, $4)",
		orgID, orgID.String()[:8], userID, now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, orgID, projID.String()[:8], userID, now)
	require.NoError(t, err)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

func seedPgProject(t *testing.T, sqlDB *sql.DB, prefix string, tc tenant.Context) uuid.UUID {
	t.Helper()
	projID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, $3, 'Docs', $4, $5, $5)",
		projID, tc.OrgID, projID.String()[:8], tc.UserID, now)
	require.NoError(t, err)
	return projID
}

func TestPostgres_APIKey_SaveAndByID(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	resourceIDs := []uuid.UUID{uuid.New(), uuid.New()}
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
	k, plaintext, err := apikey.Scheme{}.Mint(f.tc.OrgID, f.tc.ProjectID, "CI key",
		[]string{authn.ScopeSheetsRead, authn.ScopeSheetsWrite}, resourceIDs, &expiresAt, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, k))

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Equal(t, k.ID, got.ID)
	assert.Equal(t, f.tc.OrgID, got.OrgID)
	assert.Equal(t, f.tc.ProjectID, got.ProjectID)
	assert.Equal(t, "CI key", got.Name)
	assert.ElementsMatch(t, []string{authn.ScopeSheetsRead, authn.ScopeSheetsWrite}, got.Scopes)
	assert.ElementsMatch(t, resourceIDs, got.ResourceIDs)
	assert.Equal(t, k.SecretHash, got.SecretHash)
	require.NotNil(t, got.ExpiresAt)
	assert.True(t, got.ExpiresAt.Equal(expiresAt), "ExpiresAt round-trip: got=%v want=%v", got.ExpiresAt, expiresAt)
	assert.Nil(t, got.RevokedAt)
	assert.Nil(t, got.LastUsedAt)
	// NOTE: postgres timestamptz is microsecond precision, so a nanosecond-precision time.Now() does not survive the round trip.
	assert.True(t, got.CreatedAt.Equal(k.CreatedAt.Truncate(time.Microsecond)),
		"CreatedAt round-trip: got=%v want=%v", got.CreatedAt, k.CreatedAt.Truncate(time.Microsecond))
	assert.True(t, got.Matches(plaintext))
}

func TestPostgres_APIKey_NotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	_, err := f.store.ByID(ctx, uuid.New())
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestPostgres_APIKey_SaveRoundTripsEmptyScopesAndResourceIDs(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	k, _, err := apikey.Scheme{}.Mint(f.tc.OrgID, f.tc.ProjectID, "Bare", nil, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, k))

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Scopes)
	assert.Empty(t, got.ResourceIDs)
}

func TestPostgres_APIKey_BySecretHash(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	k, plaintext, err := apikey.Scheme{}.Mint(f.tc.OrgID, f.tc.ProjectID, "Hash lookup", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, k))

	got, err := f.store.BySecretHash(ctx, k.SecretHash)
	require.NoError(t, err)
	assert.Equal(t, k.ID, got.ID)
	assert.True(t, got.Matches(plaintext))
}

func TestPostgres_APIKey_BySecretHash_Miss(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	var missing [32]byte
	_, err := f.store.BySecretHash(ctx, missing)
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestPostgres_APIKey_List_ScopesToTheProject(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	otherProj := seedPgProject(t, f.sqlDB, f.prefix, f.tc)

	mine, _, err := apikey.Scheme{}.Mint(f.tc.OrgID, f.tc.ProjectID, "Mine", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, mine))

	theirs, _, err := apikey.Scheme{}.Mint(f.tc.OrgID, otherProj, "Theirs", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, theirs))

	got, err := f.store.List(ctx, f.tc.ProjectID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, mine.ID, got[0].ID)
}

func TestPostgres_APIKey_TouchLastUsed(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	k, _, err := apikey.Scheme{}.Mint(f.tc.OrgID, f.tc.ProjectID, "Touched", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, k))

	at := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, f.store.TouchLastUsed(ctx, k.ID, at))

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.Equal(at), "LastUsedAt round-trip: got=%v want=%v", got.LastUsedAt, at)
}

func TestPostgres_APIKey_TouchLastUsed_NotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	err := f.store.TouchLastUsed(ctx, uuid.New(), time.Now())
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
}

// NOTE: binds the store to a NOBYPASSRLS app role, so the RLS policies actually apply.
func newPgRLSFixture(t *testing.T) (apikey.Store, *sql.DB, *sql.DB, string) {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	ownerRole := "opensheet_keyowner_" + suffix
	appRole := "opensheet_keyapp_" + suffix
	pfx := "k" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	createRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	createRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)

	for _, stmt := range []string{
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[2]q`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %[2]q`,
	} {
		_, err = admin.ExecContext(t.Context(), fmt.Sprintf(stmt, ownerRole, appRole))
		require.NoError(t, err)
	}

	migDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migDB.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverPostgres
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = pfx
	cfg.DB.AllowBypassRLS = false
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	appConn, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 2,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appConn.Close() })

	store := apikey.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: pfx},
		db.Pool{W: appConn, R: appConn},
		tenant.NewPgConn(appConn),
	)
	return store, migDB, appConn, pfx
}

func uniqueSuffix(t *testing.T) string {
	t.Helper()
	s, err := nanoid.New(10)
	require.NoError(t, err)
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func createRole(t *testing.T, admin *sql.DB, name, attrs string) {
	t.Helper()
	_, err := admin.ExecContext(t.Context(), fmt.Sprintf(`CREATE ROLE %q %s`, name, attrs))
	require.NoError(t, err)
	// NOTE: t.Context() is already canceled by the time cleanups run, so teardown needs its own context.
	t.Cleanup(func() {
		_, dropErr := admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %q`, name))
		require.NoError(t, dropErr, "leaked objects owned by %s", name)
		_, dropErr = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %q`, name))
		require.NoError(t, dropErr, "leaked role %s", name)
	})
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT %q TO CURRENT_USER`, name))
	require.NoError(t, err)
}

func seedRLSTenant(t *testing.T, migDB *sql.DB, prefix string) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	_, err := migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@example.com", now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, 'Org', $3, $4, $4)",
		orgID, orgID.String()[:8], userID, now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"memberships (id, org_id, user_id, role, created_at) VALUES ($1, $2, $3, 'owner', $4)",
		uuid.New(), orgID, userID, now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, orgID, projID.String()[:8], userID, now)
	require.NoError(t, err)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

// TestPostgres_APIKey_OtherOrgIsInvisible is the regression detector for the RLS policy under a NOBYPASSRLS role.
func TestPostgres_APIKey_OtherOrgIsInvisible(t *testing.T) {
	store, migDB, appConn, prefix := newPgRLSFixture(t)
	a := seedRLSTenant(t, migDB, prefix)
	b := seedRLSTenant(t, migDB, prefix)

	ownerCtx := tenant.Into(t.Context(), a)
	otherCtx := tenant.Into(t.Context(), b)

	k, plaintext, err := apikey.Scheme{}.Mint(a.OrgID, a.ProjectID, "Org A Only", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, store.Save(ownerCtx, k))

	_, err = store.ByID(otherCtx, k.ID)
	assert.True(t, apikey.IsNotFoundError(err), "org B must not see org A's key, got %T: %v", err, err)

	listed, err := store.List(otherCtx, a.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, listed, "naming org A's project from org B's context must list nothing")

	assert.True(t, apikey.IsNotFoundError(store.TouchLastUsed(otherCtx, k.ID, time.Now())),
		"org B must not be able to touch org A's key")

	// SECURITY: read the table directly, bypassing the store, to prove the policy itself filters.
	tx, err := tenant.NewPgConn(appConn).BeginTenanted(t.Context(), b)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	var rows int
	require.NoError(t, tx.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+prefix+"api_keys WHERE id = $1", k.ID).Scan(&rows))
	assert.Zero(t, rows, "org A's key must be invisible under org B's tenant scope")

	// SECURITY: BySecretHash bypasses RLS via SECURITY DEFINER, as the pre-tenant lookup cannot know the org yet.
	byHash, err := store.BySecretHash(otherCtx, k.SecretHash)
	require.NoError(t, err, "BySecretHash resolves before any tenant scope is known")
	assert.Equal(t, k.ID, byHash.ID)
	assert.True(t, byHash.Matches(plaintext))

	stillThere, err := store.ByID(ownerCtx, k.ID)
	require.NoError(t, err)
	assert.Equal(t, k.ID, stillThere.ID)
}
