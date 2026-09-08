//go:build integration

package spreadsheet_test

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

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type pgTenant struct {
	tc           tenant.Context
	credentialID uuid.UUID
}

// pgFixture binds a Store to a NOBYPASSRLS app role, so every assertion below tests RLS and not just the WHERE clause.
type pgFixture struct {
	store  spreadsheet.Store
	owner  *sql.DB
	prefix string
	a      pgTenant
	b      pgTenant
}

// NOTE: pgtest reuses TEST_PG_DSN when set, so role and table names must be unique per run.
func uniqueSpreadsheetSuffix(t *testing.T) string {
	t.Helper()
	s, err := nanoid.New(10)
	require.NoError(t, err)
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func createSpreadsheetRole(t *testing.T, admin *sql.DB, name, attrs string) {
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

func newPgFixture(t *testing.T) *pgFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueSpreadsheetSuffix(t)
	ownerRole := "opensheet_sprowner_" + suffix
	appRole := "opensheet_sprapp_" + suffix
	prefix := "t" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	createSpreadsheetRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	// NOTE: USAGE as well as CREATE — without USAGE the schema drops out of search_path and DDL fails with 3F000.
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	createSpreadsheetRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)

	for _, stmt := range []string{
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[2]q`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %[2]q`,
	} {
		_, err = admin.ExecContext(t.Context(), fmt.Sprintf(stmt, ownerRole, appRole))
		require.NoError(t, err)
	}

	owner, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.DB.AllowBypassRLS = false
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), owner, cfg))

	f := &pgFixture{owner: owner, prefix: prefix}
	f.a = seedPgTenant(t, owner, prefix, "a"+suffix)
	f.b = seedPgTenant(t, owner, prefix, "b"+suffix)

	// MaxOpenConns 1 keeps every statement on the one connection whose GUC the tenanted tx sets.
	appConn, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appConn.Close() })

	f.store = spreadsheet.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix},
		db.Pool{W: appConn, R: appConn},
		tenant.NewPgConn(appConn),
	)
	return f
}

func seedPgTenant(t *testing.T, owner *sql.DB, prefix, slug string) pgTenant {
	t.Helper()
	out := pgTenant{
		tc: tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()},
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := owner.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
		out.tc.UserID, out.tc.UserID.String()+"@x.co", now)
	require.NoError(t, err)
	_, err = owner.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"orgs (id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,'Acme',false,$3,$4,$4)",
		out.tc.OrgID, "org-"+slug, out.tc.UserID, now)
	require.NoError(t, err)
	_, err = owner.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,$3,'Web',$4,$5,$5)",
		out.tc.ProjectID, out.tc.OrgID, "proj-"+slug, out.tc.UserID, now)
	require.NoError(t, err)

	out.credentialID = seedPgCredential(t, owner, prefix, out.tc, "primary")
	return out
}

func seedPgCredential(t *testing.T, owner *sql.DB, prefix string, tc tenant.Context, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := owner.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"credentials "+
			"(id, org_id, project_id, name, kind, status, authorized_by_user_id, google_account_email, sealed, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,'service_account','active',$5,'',$6,$7,$7)",
		id, tc.OrgID, tc.ProjectID, name, tc.UserID, []byte{0x00}, now)
	require.NoError(t, err)
	return id
}

func (f *pgFixture) ctxA() context.Context { return tenant.Into(context.Background(), f.a.tc) }
func (f *pgFixture) ctxB() context.Context { return tenant.Into(context.Background(), f.b.tc) }

func (f *pgFixture) save(t *testing.T, ctx context.Context, ten pgTenant, fileID, title string) *spreadsheet.Spreadsheet {
	t.Helper()
	sp, err := spreadsheet.New(ten.tc.OrgID, ten.tc.ProjectID, ten.credentialID, fileID, title)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, sp))
	return sp
}

func TestPostgres_Spreadsheet_SaveAndLookup(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()
	sp := f.save(t, ctx, f.a, "1AbC-_dEf", "Prices")

	byID, err := f.store.ByID(ctx, sp.ID)
	require.NoError(t, err)
	assert.Equal(t, "Prices", byID.Title)
	assert.Equal(t, f.a.credentialID, byID.CredentialID)
	assert.Equal(t, time.UTC, byID.CreatedAt.Location())

	byFileID, err := f.store.ByGoogleFileID(ctx, f.a.tc.OrgID, f.a.tc.ProjectID, "1AbC-_dEf")
	require.NoError(t, err)
	assert.Equal(t, sp.ID, byFileID.ID)
}

func TestPostgres_Spreadsheet_NotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()

	_, err := f.store.ByID(ctx, uuid.New())
	assert.True(t, spreadsheet.IsNotFoundError(err), "ByID: want NotFoundError, got %T: %v", err, err)

	_, err = f.store.ByGoogleFileID(ctx, f.a.tc.OrgID, f.a.tc.ProjectID, "MISSING")
	assert.True(t, spreadsheet.IsNotFoundError(err), "ByGoogleFileID: want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Spreadsheet_List(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()
	first := f.save(t, ctx, f.a, "AAA", "A")
	second := f.save(t, ctx, f.a, "BBB", "B")

	got, err := f.store.List(ctx, f.a.tc.OrgID, f.a.tc.ProjectID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, first.ID, got[0].ID, "List must be oldest first")
	assert.Equal(t, second.ID, got[1].ID)
}

func TestPostgres_Spreadsheet_SaveIsUpsert(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()
	sp := f.save(t, ctx, f.a, "1AbC-_dEf", "Prices")

	nextCred := seedPgCredential(t, f.owner, f.prefix, f.a.tc, "secondary")
	require.NoError(t, sp.Retitle("Renamed"))
	require.NoError(t, sp.Rebind(nextCred))
	require.NoError(t, f.store.Save(ctx, sp))

	rows, err := f.store.List(ctx, f.a.tc.OrgID, f.a.tc.ProjectID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "Renamed", rows[0].Title)
	assert.Equal(t, nextCred, rows[0].CredentialID)
}

func TestPostgres_Spreadsheet_DuplicateGoogleFileIDIsAlreadyExists(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()
	f.save(t, ctx, f.a, "1AbC-_dEf", "Prices")

	dup, err := spreadsheet.New(f.a.tc.OrgID, f.a.tc.ProjectID, f.a.credentialID, "1AbC-_dEf", "Prices again")
	require.NoError(t, err)
	err = f.store.Save(ctx, dup)
	assert.True(t, spreadsheet.IsAlreadyExistsError(err), "want AlreadyExistsError, got %T: %v", err, err)

	rows, listErr := f.store.List(ctx, f.a.tc.OrgID, f.a.tc.ProjectID)
	require.NoError(t, listErr)
	assert.Len(t, rows, 1, "the duplicate must not land as a second row")
}

func TestPostgres_Spreadsheet_SameDocumentInTwoProjectsIsAllowed(t *testing.T) {
	f := newPgFixture(t)
	f.save(t, f.ctxA(), f.a, "1AbC-_dEf", "Prices")
	f.save(t, f.ctxB(), f.b, "1AbC-_dEf", "Prices")

	rowsA, err := f.store.List(f.ctxA(), f.a.tc.OrgID, f.a.tc.ProjectID)
	require.NoError(t, err)
	assert.Len(t, rowsA, 1)
	rowsB, err := f.store.List(f.ctxB(), f.b.tc.OrgID, f.b.tc.ProjectID)
	require.NoError(t, err)
	assert.Len(t, rowsB, 1)
}

func TestPostgres_Spreadsheet_Delete(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()
	sp := f.save(t, ctx, f.a, "1AbC-_dEf", "Prices")

	require.NoError(t, f.store.Delete(ctx, sp.ID))
	_, err := f.store.ByID(ctx, sp.ID)
	assert.True(t, spreadsheet.IsNotFoundError(err), "want NotFoundError after delete, got %T: %v", err, err)

	err = f.store.Delete(ctx, sp.ID)
	assert.True(t, spreadsheet.IsNotFoundError(err), "second Delete: want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Spreadsheet_DeleteCascadesSheets(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctxA()
	sp := f.save(t, ctx, f.a, "1AbC-_dEf", "Prices")

	sheetID := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := f.owner.ExecContext(t.Context(),
		"INSERT INTO public."+f.prefix+"sheets "+
			"(id, org_id, project_id, spreadsheet_id, tab, slug, visibility, cache_ttl_secs, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,'Q1','prices','key',0,$5,$5)",
		sheetID, f.a.tc.OrgID, f.a.tc.ProjectID, sp.ID, now)
	require.NoError(t, err)

	require.NoError(t, f.store.Delete(ctx, sp.ID))

	var n int
	require.NoError(t, f.owner.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheets WHERE id = $1", sheetID).Scan(&n))
	assert.Zero(t, n, "the FK must cascade published sheets away with the spreadsheet")
}

// TestPostgres_Spreadsheet_RLSHidesAnotherOrg asserts a NOBYPASSRLS role sees zero rows outside its own org,
// even when it asks for another org's ids by name.
func TestPostgres_Spreadsheet_RLSHidesAnotherOrg(t *testing.T) {
	f := newPgFixture(t)
	spB := f.save(t, f.ctxB(), f.b, "1AbC-_dEf", "B's prices")

	ctxA := f.ctxA()

	_, err := f.store.ByID(ctxA, spB.ID)
	assert.True(t, spreadsheet.IsNotFoundError(err),
		"org A must not read org B's row by id, got %T: %v", err, err)

	_, err = f.store.ByGoogleFileID(ctxA, f.b.tc.OrgID, f.b.tc.ProjectID, "1AbC-_dEf")
	assert.True(t, spreadsheet.IsNotFoundError(err),
		"org A must not read org B's row by file id, got %T: %v", err, err)

	rows, err := f.store.List(ctxA, f.b.tc.OrgID, f.b.tc.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, rows, "org A must list zero of org B's rows")

	err = f.store.Delete(ctxA, spB.ID)
	assert.True(t, spreadsheet.IsNotFoundError(err),
		"org A must not delete org B's row, got %T: %v", err, err)

	var n int
	require.NoError(t, f.owner.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"spreadsheets WHERE id = $1", spB.ID).Scan(&n))
	assert.Equal(t, 1, n, "org B's row must survive org A's delete attempt")
}

// TestPostgres_Spreadsheet_WriteOutsideScopeIsRefused asserts RLS blocks an insert stamped with another org's id.
func TestPostgres_Spreadsheet_WriteOutsideScopeIsRefused(t *testing.T) {
	f := newPgFixture(t)

	forged, err := spreadsheet.New(f.b.tc.OrgID, f.b.tc.ProjectID, f.b.credentialID, "FORGED", "not mine")
	require.NoError(t, err)

	err = f.store.Save(f.ctxA(), forged)
	require.Error(t, err, "RLS must refuse a row stamped with another org's id")

	var n int
	require.NoError(t, f.owner.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"spreadsheets WHERE google_file_id = 'FORGED'").Scan(&n))
	assert.Zero(t, n)
}

func TestPostgres_Spreadsheet_TenantMissing(t *testing.T) {
	f := newPgFixture(t)
	bare := context.Background()

	_, err := f.store.ByID(bare, uuid.New())
	assert.True(t, tenant.IsMissingError(err), "ByID: want tenant.MissingError, got %T: %v", err, err)

	_, err = f.store.List(bare, f.a.tc.OrgID, f.a.tc.ProjectID)
	assert.True(t, tenant.IsMissingError(err), "List: want tenant.MissingError, got %T: %v", err, err)

	assert.True(t, tenant.IsMissingError(f.store.Delete(bare, uuid.New())),
		"Delete: want tenant.MissingError")
}
