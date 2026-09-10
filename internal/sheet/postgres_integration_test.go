//go:build integration

package sheet_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type orgTree struct {
	userID        uuid.UUID
	orgID         uuid.UUID
	projectID     uuid.UUID
	spreadsheetID uuid.UUID
}

func (o orgTree) ctx(t *testing.T) context.Context {
	t.Helper()
	return tenant.Into(t.Context(), tenant.Context{
		OrgID: o.orgID, ProjectID: o.projectID, UserID: o.userID,
	})
}

type pgFixture struct {
	store     sheet.Store
	snapshots sheet.SnapshotStore
	attempts  sheet.IdempotencyStore
	appDB     *sql.DB
	ownerDB   *sql.DB
	prefix    string
	a         orgTree
	b         orgTree
}

// newPgFixture migrates as a BYPASSRLS owner and hands back a store bound to a NOBYPASSRLS app role, so RLS actually applies.
func newPgFixture(t *testing.T) *pgFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := pgUniqueSuffix(t)
	ownerRole := "opensheet_shtowner_" + suffix
	appRole := "opensheet_shtapp_" + suffix
	prefix := "s" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	pgCreateRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	migDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migDB.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	f := &pgFixture{prefix: prefix, ownerDB: migDB}
	f.a = pgSeedOrgTree(t, migDB, prefix, "acme-"+suffix, "gfile-a-"+suffix)
	f.b = pgSeedOrgTree(t, migDB, prefix, "globex-"+suffix, "gfile-b-"+suffix)

	pgCreateRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)
	for _, table := range []string{prefix + "sheets", prefix + "sheet_snapshots", prefix + "sheet_write_attempts"} {
		_, err = migDB.ExecContext(t.Context(),
			fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON public.%s TO %q`, table, appRole))
		require.NoError(t, err)
	}

	appDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	var bypass bool
	require.NoError(t, appDB.QueryRowContext(t.Context(),
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the store must run as a NOBYPASSRLS role or the RLS assertions below prove nothing")

	f.appDB = appDB
	dbCfg := db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix}
	pool := db.Pool{W: appDB, R: appDB}
	pc := tenant.NewPgConn(appDB)
	f.store = sheet.NewStore(dbCfg, pool, pc)
	f.snapshots, err = sheet.NewSnapshotStore(
		config.CacheConfig{Driver: config.CacheDriverPostgres, MaxBytes: 1 << 20},
		dbCfg, pc, slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)

	// NOTE: its own pool, because appDB caps at one connection and the concurrent-Reserve test needs real overlap rather than pool-level serialization.
	attemptsDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 4,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = attemptsDB.Close() })
	f.attempts = sheet.NewIdempotencyStore(dbCfg, tenant.NewPgConn(attemptsDB))
	return f
}

// NOTE: pgtest reuses TEST_PG_DSN when set, so role and table names must be unique per run.
func pgUniqueSuffix(t *testing.T) string {
	t.Helper()
	s, err := nanoid.New(10)
	require.NoError(t, err)
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func pgCreateRole(t *testing.T, admin *sql.DB, name, attrs string) {
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

func pgSeedOrgTree(t *testing.T, migDB *sql.DB, prefix, orgSlug, fileID string) orgTree {
	t.Helper()
	tree := orgTree{
		userID:        uuid.New(),
		orgID:         uuid.New(),
		projectID:     uuid.New(),
		spreadsheetID: uuid.New(),
	}
	credentialID := uuid.New()
	now := time.Now().UTC()

	_, err := migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
		tree.userID, tree.userID.String()+"@x.co", now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'Org',$3,$4,$4)",
		tree.orgID, orgSlug, tree.userID, now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'web','Web',$3,$4,$4)",
		tree.projectID, tree.orgID, tree.userID, now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"credentials (id, org_id, project_id, name, kind, status, authorized_by_user_id, google_account_email, sealed, created_at, updated_at) "+
			"VALUES ($1,$2,$3,'primary','service_account','active',$4,'',$5,$6,$6)",
		credentialID, tree.orgID, tree.projectID, tree.userID, []byte("sealed"), now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, title, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,$5,'Rates',$6,$6)",
		tree.spreadsheetID, tree.orgID, tree.projectID, credentialID, fileID, now)
	require.NoError(t, err)

	return tree
}

func TestPostgres_Sheet_SaveAndByID(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	want, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "Kamar", Slug: "prices", Visibility: sheet.VisibilityPublic, CacheTTL: 90 * time.Second})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, want))

	got, err := f.store.ByID(ctx, want.ID)
	require.NoError(t, err)
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, f.a.spreadsheetID, got.SpreadsheetID)
	assert.Equal(t, "Kamar", got.Tab)
	assert.Equal(t, "prices", got.Slug)
	assert.Equal(t, sheet.VisibilityPublic, got.Visibility)
	assert.Equal(t, 90*time.Second, got.CacheTTL, "cache_ttl_secs must come back as seconds, not nanoseconds")
	assert.True(t, got.CreatedAt.Equal(want.CreatedAt), "CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
}

func TestPostgres_Sheet_CacheTTLRoundTripsInSeconds(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	for name, ttl := range map[string]time.Duration{
		"90s":  90 * time.Second,
		"zero": 0,
		"1s":   time.Second,
		"24h":  24 * time.Hour,
	} {
		t.Run(name, func(t *testing.T) {
			sh, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "", Slug: "ttl-" + strings.ToLower(name), Visibility: sheet.VisibilityKey, CacheTTL: ttl})
			require.NoError(t, err)
			require.NoError(t, f.store.Save(ctx, sh))

			got, err := f.store.ByID(ctx, sh.ID)
			require.NoError(t, err)
			assert.Equal(t, ttl, got.CacheTTL)
		})
	}
}

func TestPostgres_Sheet_WritableRoundTrips(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	for name, writable := range map[string]bool{"writable": true, "not-writable": false} {
		t.Run(name, func(t *testing.T) {
			sh, err := sheet.New(sheet.NewParams{
				OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID,
				Slug: "w-" + name, Visibility: sheet.VisibilityKey, Writable: writable,
			})
			require.NoError(t, err)
			require.NoError(t, f.store.Save(ctx, sh))

			got, err := f.store.ByID(ctx, sh.ID)
			require.NoError(t, err)
			assert.Equal(t, writable, got.Writable, "writable must survive save then load")
		})
	}
}

func TestPostgres_Sheet_WritableSurvivesAnUpsert(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	sh, err := sheet.New(sheet.NewParams{
		OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID,
		Slug: "prices", Visibility: sheet.VisibilityKey,
	})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, sh))
	assert.False(t, sh.Writable)

	sh.SetWritable(true)
	require.NoError(t, f.store.Save(ctx, sh))

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.True(t, got.Writable, "the ON CONFLICT DO UPDATE branch must carry writable")
}

func TestPostgres_Sheet_NotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	_, err := f.store.ByID(ctx, uuid.New())
	assert.True(t, sheet.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)

	_, err = f.store.BySlug(ctx, f.a.orgID, f.a.projectID, "absent")
	assert.True(t, sheet.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Sheet_SameSlugInTwoProjectsResolvesToDifferentSheets(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	otherProject := uuid.New()
	otherCredential := uuid.New()
	otherSpreadsheet := uuid.New()
	now := time.Now().UTC()
	adminDB := f.ownerDB
	_, err := adminDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'mobile','Mobile',$3,$4,$4)",
		otherProject, f.a.orgID, f.a.userID, now)
	require.NoError(t, err)
	_, err = adminDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"credentials (id, org_id, project_id, name, kind, status, authorized_by_user_id, google_account_email, sealed, created_at, updated_at) "+
			"VALUES ($1,$2,$3,'mobile-primary','service_account','active',$4,'',$5,$6,$6)",
		otherCredential, f.a.orgID, otherProject, f.a.userID, []byte("sealed"), now)
	require.NoError(t, err)
	_, err = adminDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, title, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,'gfile-mobile','Rates',$5,$5)",
		otherSpreadsheet, f.a.orgID, otherProject, otherCredential, now)
	require.NoError(t, err)

	ctxOther := tenant.Into(t.Context(), tenant.Context{
		OrgID: f.a.orgID, ProjectID: otherProject, UserID: f.a.userID,
	})

	a, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "Kamar A", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	b, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: otherProject, SpreadsheetID: otherSpreadsheet, Tab: "Kamar B", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, a))
	require.NoError(t, f.store.Save(ctxOther, b), "the same slug must be free in another project")

	gotA, err := f.store.BySlug(ctx, f.a.orgID, f.a.projectID, "prices")
	require.NoError(t, err)
	gotB, err := f.store.BySlug(ctxOther, f.a.orgID, otherProject, "prices")
	require.NoError(t, err)

	assert.Equal(t, a.ID, gotA.ID)
	assert.Equal(t, b.ID, gotB.ID)
	assert.Equal(t, "Kamar A", gotA.Tab)
	assert.Equal(t, "Kamar B", gotB.Tab)
}

func TestPostgres_Sheet_SlugIsUniquePerProject(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	first, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, first))

	second, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)

	err = f.store.Save(ctx, second)
	assert.True(t, sheet.IsAlreadyExistsError(err), "want AlreadyExistsError, got %T: %v", err, err)
}

func TestPostgres_Sheet_SaveIsUpsert(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	sh, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "Kamar", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, sh))

	sh.Retab("Harga")
	require.NoError(t, sh.Publish(sheet.VisibilityPublic))
	require.NoError(t, sh.SetTTL(2*time.Hour))
	require.NoError(t, f.store.Save(ctx, sh))

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.Equal(t, "Harga", got.Tab)
	assert.Equal(t, sheet.VisibilityPublic, got.Visibility)
	assert.Equal(t, 2*time.Hour, got.CacheTTL)

	all, err := f.store.List(ctx, f.a.orgID, f.a.projectID)
	require.NoError(t, err)
	assert.Len(t, all, 1, "an upsert must not insert a second row")
}

func TestPostgres_Sheet_ListAndDelete(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	ids := map[string]uuid.UUID{}
	for _, slug := range []string{"rooms", "prices"} {
		sh, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "", Slug: slug, Visibility: sheet.VisibilityKey, CacheTTL: 0})
		require.NoError(t, err)
		require.NoError(t, f.store.Save(ctx, sh))
		ids[slug] = sh.ID
	}

	got, err := f.store.List(ctx, f.a.orgID, f.a.projectID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "prices", got[0].Slug, "List orders by slug ascending")
	assert.Equal(t, "rooms", got[1].Slug)

	require.NoError(t, f.store.Delete(ctx, ids["rooms"]))
	_, err = f.store.ByID(ctx, ids["rooms"])
	assert.True(t, sheet.IsNotFoundError(err), "want NotFoundError after delete, got %T: %v", err, err)

	err = f.store.Delete(ctx, ids["rooms"])
	assert.True(t, sheet.IsNotFoundError(err), "double delete want NotFoundError, got %T: %v", err, err)
}

// TestPostgres_Sheet_AnotherOrgSeesZeroRows asserts RLS hides one org's sheets from another under a NOBYPASSRLS role.
func TestPostgres_Sheet_AnotherOrgSeesZeroRows(t *testing.T) {
	f := newPgFixture(t)
	ctxA, ctxB := f.a.ctx(t), f.b.ctx(t)

	shA, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "Kamar A", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctxA, shA))

	shB, err := sheet.New(sheet.NewParams{OrgID: f.b.orgID, ProjectID: f.b.projectID, SpreadsheetID: f.b.spreadsheetID, Tab: "Kamar B", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctxB, shB))

	rowsB, err := f.store.List(ctxB, f.a.orgID, f.a.projectID)
	require.NoError(t, err)
	assert.Empty(t, rowsB, "org B must read zero rows from org A's project")

	_, err = f.store.ByID(ctxB, shA.ID)
	assert.True(t, sheet.IsNotFoundError(err), "cross-org ByID want NotFoundError, got %T: %v", err, err)

	_, err = f.store.BySlug(ctxB, f.a.orgID, f.a.projectID, "prices")
	assert.True(t, sheet.IsNotFoundError(err), "cross-org BySlug want NotFoundError, got %T: %v", err, err)

	err = f.store.Delete(ctxB, shA.ID)
	assert.True(t, sheet.IsNotFoundError(err), "cross-org Delete want NotFoundError, got %T: %v", err, err)

	stillThere, err := f.store.ByID(ctxA, shA.ID)
	require.NoError(t, err, "org A lost its sheet to a cross-org delete")
	assert.Equal(t, shA.ID, stillThere.ID)

	var unscoped int
	require.NoError(t, f.appDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheets").Scan(&unscoped))
	assert.Zero(t, unscoped, "an unscoped read must return zero rows under FORCE ROW LEVEL SECURITY")
}

func TestPostgres_Sheet_RequiresTenantScope(t *testing.T) {
	f := newPgFixture(t)

	sh, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)

	assert.True(t, tenant.IsMissingError(f.store.Save(t.Context(), sh)))
	_, err = f.store.ByID(t.Context(), sh.ID)
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.BySlug(t.Context(), f.a.orgID, f.a.projectID, "prices")
	assert.True(t, tenant.IsMissingError(err))
	_, err = f.store.List(t.Context(), f.a.orgID, f.a.projectID)
	assert.True(t, tenant.IsMissingError(err))
	assert.True(t, tenant.IsMissingError(f.store.Delete(t.Context(), sh.ID)))
}

func TestPostgres_Sheet_SaveRoundTripsTheProjectionColumns(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	validatedAt := time.Now().UTC().Truncate(time.Microsecond)
	sh, err := sheet.New(sheet.NewParams{
		OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID,
		Slug: "projection", Visibility: sheet.VisibilityKey,
	})
	require.NoError(t, err)
	require.True(t, sh.ContractOK, "New must default contract_ok to true")
	sh.Generation = 7
	sh.ValidatedAt = &validatedAt
	sh.ContractReason = "checked"
	require.NoError(t, f.store.Save(ctx, sh))

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 7, got.Generation, "generation must have a matching alias field on pgSheetRow")
	require.NotNil(t, got.ValidatedAt, "validated_at must have a matching alias field on pgSheetRow")
	assert.True(t, got.ValidatedAt.Equal(validatedAt), "ValidatedAt = %v, want %v", got.ValidatedAt, validatedAt)
	assert.True(t, got.ContractOK)
	assert.Equal(t, "checked", got.ContractReason)
}

func TestPostgres_Sheet_UpdateDoesNotResetGeneration(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	sh, err := sheet.New(sheet.NewParams{
		OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID,
		Slug: "prices", Visibility: sheet.VisibilityKey,
	})
	require.NoError(t, err)
	sh.Generation = 7
	require.NoError(t, f.store.Save(ctx, sh))

	sh.Generation = 0
	sh.Retab("Harga")
	require.NoError(t, f.store.Save(ctx, sh))

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.Equal(t, "Harga", got.Tab, "the upsert must still apply the metadata edit")
	assert.EqualValues(t, 7, got.Generation, "generation must not be in MutableColumns")
}
