//go:build integration

package webhook_test

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
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/internal/webhook"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type pgFixture struct {
	store  webhook.Store
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
	store := webhook.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: pfx},
		db.Pool{W: sqlDB, R: sqlDB},
		tenant.NewPgConn(sqlDB),
	)
	return pgFixture{store: store, sqlDB: sqlDB, prefix: pfx, tc: seedPgTenant(t, sqlDB, pfx)}
}

func seedPgTenant(t *testing.T, sqlDB *sql.DB, prefix string) tenant.Context {
	t.Helper()
	userID, orgID := uuid.New(), uuid.New()
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
	tc := tenant.Context{OrgID: orgID, UserID: userID}
	tc.ProjectID = seedPgProject(t, sqlDB, prefix, tc)
	return tc
}

func seedPgProject(t *testing.T, sqlDB *sql.DB, prefix string, tc tenant.Context) uuid.UUID {
	t.Helper()
	projID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, tc.OrgID, projID.String()[:8], tc.UserID, now)
	require.NoError(t, err)
	return projID
}

func TestPostgres_SaveAndByID(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	e := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, e))

	got, err := f.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assertSameEndpoint(t, e, got)
	assert.Nil(t, got.Secrets.Secondary, "a NULL secondary must read back as nil")
}

func TestPostgres_SaveWritesAnEmptySecondaryAsNull(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	e := newSealedEndpoint(t, f.tc)
	e.Secrets.Secondary = []byte{}
	require.NoError(t, f.store.Save(ctx, e), "an empty secondary must not trip the length CHECK on insert")
	require.NoError(t, f.store.Save(ctx, e), "nor on the conflict update")

	got, err := f.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Secrets.Secondary)
	var nulls int
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM "+f.prefix+"webhook_endpoints WHERE id = $1 AND secret_secondary IS NULL", e.ID).Scan(&nulls))
	assert.Equal(t, 1, nulls)
}

func TestPostgres_SaveAttemptForADeletedEndpointIsNotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	e := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, e))
	require.NoError(t, f.store.Delete(ctx, e.ID))

	err := f.store.SaveAttempt(ctx, newAttempt(f.tc, e.ID, uuid.New(), 1, time.Now()))
	assert.True(t, webhook.IsNotFoundError(err), "the endpoint FK must surface as NotFoundError, got %T: %v", err, err)
}

func TestPostgres_ByID_NotFound(t *testing.T) {
	f := newPgFixture(t)
	_, err := f.store.ByID(tenant.Into(t.Context(), f.tc), uuid.New())
	assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestPostgres_List_OrdersByCreatedAtThenID(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	older := newSealedEndpoint(t, f.tc)
	older.CreatedAt = base
	tieA := newSealedEndpoint(t, f.tc)
	tieA.CreatedAt = base.Add(time.Minute)
	tieB := newSealedEndpoint(t, f.tc)
	tieB.CreatedAt = base.Add(time.Minute)
	for _, e := range []*webhook.Endpoint{older, tieA, tieB} {
		require.NoError(t, f.store.Save(ctx, e))
	}
	otherProject := f.tc
	otherProject.ProjectID = seedPgProject(t, f.sqlDB, f.prefix, f.tc)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), otherProject), newSealedEndpoint(t, otherProject)))

	got, err := f.store.List(ctx, f.tc.OrgID, f.tc.ProjectID)
	require.NoError(t, err)
	require.Len(t, got, 3, "the list must scope to the project")

	first, second := tieA, tieB
	if tieB.ID.String() > tieA.ID.String() {
		first, second = tieB, tieA
	}
	assert.Equal(t, []uuid.UUID{first.ID, second.ID, older.ID}, []uuid.UUID{got[0].ID, got[1].ID, got[2].ID})
}

func TestPostgres_Delete(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	e := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, e))
	delivery := uuid.New()
	require.NoError(t, f.store.SaveAttempt(ctx, newAttempt(f.tc, e.ID, delivery, 1, time.Now())))

	require.NoError(t, f.store.Delete(ctx, e.ID))
	_, err := f.store.ByID(ctx, e.ID)
	assert.True(t, webhook.IsNotFoundError(err))
	attempts, err := f.store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	assert.Empty(t, attempts, "deleting an endpoint must cascade to its attempts")
	assert.True(t, webhook.IsNotFoundError(f.store.Delete(ctx, e.ID)))
}

func TestPostgres_SaveAttemptAndListAttempts(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)

	e := newSealedEndpoint(t, f.tc)
	other := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, e))
	require.NoError(t, f.store.Save(ctx, other))

	delivery, otherDelivery := uuid.New(), uuid.New()
	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	first := newAttempt(f.tc, e.ID, delivery, 1, base)
	second := noResponse(newAttempt(f.tc, e.ID, delivery, 2, base.Add(30*time.Second)))
	for _, a := range []webhook.Attempt{
		first, second,
		newAttempt(f.tc, e.ID, otherDelivery, 1, base),
		newAttempt(f.tc, other.ID, delivery, 1, base),
	} {
		require.NoError(t, f.store.SaveAttempt(ctx, a))
	}

	got, err := f.store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	require.Len(t, got, 2, "attempts must filter by endpoint and delivery")
	assert.Equal(t, second.ID, got[0].ID, "newest first")
	assert.Equal(t, first.ID, got[1].ID)
	assert.Equal(t, first.EventID, got[1].EventID)
	assert.Equal(t, first.EventType, got[1].EventType)
	assert.Equal(t, 500, got[1].StatusCode)
	assert.Equal(t, "boom", got[1].Error)
	assert.Equal(t, 1234*time.Millisecond, got[1].Duration)
	assert.True(t, first.CreatedAt.Equal(got[1].CreatedAt))
	assertSameResponse(t, first, got[1])
	assert.Empty(t, got[0].ResponseBody)
	assert.False(t, got[0].ResponseTruncated)
	assert.Empty(t, got[0].ResponseHeaders)
}

func TestPostgres_SaveAttemptTruncatesErrorToValidUTF8(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	e := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, e))

	delivery := uuid.New()
	a := newAttempt(f.tc, e.ID, delivery, 1, time.Now())
	a.Error = strings.Repeat("x", outbox.MaxCauseLen-1) + "é"
	require.NoError(t, f.store.SaveAttempt(ctx, a), "a rune split by the cut must not fail the statement")

	got, err := f.store.ListAttempts(ctx, e.ID, delivery)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, strings.Repeat("x", outbox.MaxCauseLen-1), got[0].Error)
}

func TestPostgres_SaveAttemptRefusesAnotherProject(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	e := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, e))

	a := newAttempt(f.tc, e.ID, uuid.New(), 1, time.Now())
	a.ProjectID = seedPgProject(t, f.sqlDB, f.prefix, f.tc)
	err := f.store.SaveAttempt(ctx, a)
	assert.True(t, webhook.IsInvalidAttemptError(err), "got %T: %v", err, err)
}

// TestPostgres_OtherOrgIsInvisible_WithoutRLS runs the isolation checks with row level security inert, leaving only the explicit org_id predicates.
func TestPostgres_OtherOrgIsInvisible_WithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	var bypass bool
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(),
		"SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user").Scan(&bypass))
	require.True(t, bypass, "this test proves nothing unless RLS is inert for this connection")

	b := seedPgTenant(t, f.sqlDB, f.prefix)
	assertOtherOrgIsInvisible(t, f.store, f.tc, b)
}

// TestPostgres_OtherOrgIsInvisible runs the isolation checks under enforced row level security.
func TestPostgres_OtherOrgIsInvisible(t *testing.T) {
	store, migDB, appConn, prefix := newPgRLSFixture(t)
	a := seedRLSTenant(t, migDB, prefix)
	b := seedRLSTenant(t, migDB, prefix)
	ownerCtx := tenant.Into(t.Context(), a)

	e := assertOtherOrgIsInvisible(t, store, a, b)

	delivery := uuid.New()
	require.NoError(t, store.SaveAttempt(ownerCtx, newAttempt(a, e.ID, delivery, 1, time.Now())))

	// SECURITY: read the tables directly under org B's scope, so only the policies stand between it and org A's rows.
	tx, err := tenant.NewPgConn(appConn).BeginTenanted(t.Context(), b)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	var endpoints, attempts int
	require.NoError(t, tx.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+prefix+"webhook_endpoints WHERE id = $1", e.ID).Scan(&endpoints))
	assert.Zero(t, endpoints, "org A's endpoint must be invisible under org B's tenant scope")
	require.NoError(t, tx.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+prefix+"webhook_deliveries WHERE endpoint_id = $1", e.ID).Scan(&attempts))
	assert.Zero(t, attempts, "org A's attempts must be invisible under org B's tenant scope")
}

func assertOtherOrgIsInvisible(t *testing.T, store webhook.Store, a, b tenant.Context) *webhook.Endpoint {
	t.Helper()
	ownerCtx := tenant.Into(t.Context(), a)
	otherCtx := tenant.Into(t.Context(), b)
	victim := newSealedEndpoint(t, a)
	require.NoError(t, store.Save(ownerCtx, victim))

	_, err := store.ByID(otherCtx, victim.ID)
	assert.True(t, webhook.IsNotFoundError(err), "org B must not see org A's endpoint, got %T: %v", err, err)

	listed, err := store.List(otherCtx, a.OrgID, a.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, listed, "naming org A's scope from org B's context must list nothing")

	hijack := newSealedEndpoint(t, b)
	hijack.ID = victim.ID
	hijack.URL = "https://attacker.example/steal"
	assert.True(t, webhook.IsNotFoundError(store.Save(otherCtx, hijack)), "org B must not upsert onto org A's endpoint")
	assert.True(t, webhook.IsNotFoundError(store.SaveSecrets(otherCtx, victim.ID, victim.Secrets,
		webhook.SealedSecrets{Primary: []byte("attacker-primary")})), "org B must not rewrite org A's secrets")

	assert.True(t, webhook.IsNotFoundError(store.Delete(otherCtx, victim.ID)), "org B must not delete org A's endpoint")

	got, err := store.ByID(ownerCtx, victim.ID)
	require.NoError(t, err)
	assert.Equal(t, validURL, got.URL, "org A's endpoint must be untouched")
	assert.Equal(t, victim.Secrets, got.Secrets, "org A's secrets must be untouched")
	return victim
}

// NOTE: binds the store to a NOBYPASSRLS app role, so the RLS policies actually apply.
func newPgRLSFixture(t *testing.T) (webhook.Store, *sql.DB, *sql.DB, string) {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	ownerRole := "opensheet_whkowner_" + suffix
	appRole := "opensheet_whkapp_" + suffix
	pfx := "w" + suffix + "_"

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

	store := webhook.NewStore(
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
		"INSERT INTO public."+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, orgID, projID.String()[:8], userID, now)
	require.NoError(t, err)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

func TestPostgres_SaveKeepsStoredSecrets(t *testing.T) {
	f := newPgFixture(t)
	assertSaveKeepsStoredSecrets(t, f.store, f.tc)
}

func TestPostgres_SaveSecretsRotatesAndClearsSecondary(t *testing.T) {
	f := newPgFixture(t)
	assertSaveSecretsRotatesAndClearsSecondary(t, f.store, f.tc)
}

func TestPostgres_SaveSecretsRefusesAStaleExpected(t *testing.T) {
	f := newPgFixture(t)
	assertSaveSecretsRefusesAStaleExpected(t, f.store, f.tc)
}
