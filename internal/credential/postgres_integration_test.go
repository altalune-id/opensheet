//go:build integration

package credential_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type pgFixture struct {
	store    credential.Store
	appConn  *sql.DB
	ownerDB  *sql.DB
	prefix   string
	userID   uuid.UUID
	orgID    uuid.UUID
	projID   uuid.UUID
	otherOrg uuid.UUID
	otherPrj uuid.UUID
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

// newPGFixture migrates as an owner role and returns a Store bound to a NOBYPASSRLS app role.
// SECURITY: a superuser connection proves nothing about RLS.
func newPGFixture(t *testing.T) *pgFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	ownerRole := "opensheet_credowner_" + suffix
	appRole := "opensheet_credapp_" + suffix
	prefix := "t" + suffix + "_"

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

	ownerDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ownerDB.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.DB.AllowBypassRLS = false
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), ownerDB, cfg))

	f := &pgFixture{ownerDB: ownerDB, prefix: prefix, userID: uuid.New()}
	f.orgID, f.projID = f.seedProjectTree(t, "acme-"+suffix)
	f.otherOrg, f.otherPrj = f.seedProjectTree(t, "rival-"+suffix)

	f.appConn, err = db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 4,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.appConn.Close() })

	f.store = credential.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix},
		db.Pool{W: f.appConn, R: f.appConn},
		tenant.NewPgConn(f.appConn),
	)
	return f
}

func (f *pgFixture) seedProjectTree(t *testing.T, slug string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orgID, projID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	if f.userID != uuid.Nil {
		_, err := f.ownerDB.ExecContext(t.Context(),
			"INSERT INTO public."+f.prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
				"VALUES ($1, $2, '', '', false, $3, $3) ON CONFLICT (id) DO NOTHING",
			f.userID, f.userID.String()+"@x.co", now)
		require.NoError(t, err)
	}
	_, err := f.ownerDB.ExecContext(t.Context(),
		"INSERT INTO public."+f.prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Acme', $3, $4, $4)",
		orgID, slug, f.userID, now)
	require.NoError(t, err)
	_, err = f.ownerDB.ExecContext(t.Context(),
		"INSERT INTO public."+f.prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'web', 'Web', $3, $4, $4)",
		projID, orgID, f.userID, now)
	require.NoError(t, err)
	return orgID, projID
}

func (f *pgFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	return tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.projID, UserID: f.userID})
}

func (f *pgFixture) newCredential(t *testing.T, name string) *credential.Credential {
	t.Helper()
	c, err := credential.New(uuid.Nil, f.orgID, f.projID, f.userID,
		name, credential.KindServiceAccount, "sa@x.iam.gserviceaccount.com", []byte{0xde, 0xad, 0xbe, 0xef})
	require.NoError(t, err)
	return c
}

func TestPostgres_Credential_AppRoleIsNotBypassRLS(t *testing.T) {
	f := newPGFixture(t)

	var bypass bool
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the app role must not hold BYPASSRLS or every case below proves nothing")

	var direct int
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"credentials").Scan(&direct))
	require.Zero(t, direct, "an unscoped read must see nothing under FORCE row level security")
}

func TestPostgres_Credential_SaveAndByID(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.ctx(t)
	c := f.newCredential(t, "prod reader")
	require.NoError(t, f.store.Save(ctx, c))

	got, err := f.store.ByID(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, "prod reader", got.Name)
	require.Equal(t, credential.KindServiceAccount, got.Kind)
	require.Equal(t, credential.StatusActive, got.Status)
	require.Equal(t, f.orgID, got.OrgID)
	require.Equal(t, f.projID, got.ProjectID)
	require.Equal(t, f.userID, got.AuthorizedByUserID)
	require.Equal(t, c.Sealed, got.Sealed)
	require.WithinDuration(t, c.CreatedAt, got.CreatedAt, time.Millisecond)
}

func TestPostgres_Credential_NotFound(t *testing.T) {
	f := newPGFixture(t)
	_, err := f.store.ByID(f.ctx(t), uuid.New())
	require.True(t, credential.IsNotFoundError(err), "want *NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Credential_SaveIsUpsert(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.ctx(t)
	c := f.newCredential(t, "prod")
	require.NoError(t, f.store.Save(ctx, c))

	require.NoError(t, c.Rotate([]byte{0x01, 0x02}, "next@x.com"))
	c.MarkReauthNeeded()
	require.NoError(t, f.store.Save(ctx, c))

	got, err := f.store.ByID(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, credential.StatusReauthNeeded, got.Status)
	require.Equal(t, []byte{0x01, 0x02}, got.Sealed)
	require.Equal(t, "next@x.com", got.GoogleAccountEmail)
}

func TestPostgres_Credential_DuplicateNameIsAlreadyExists(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.ctx(t)
	require.NoError(t, f.store.Save(ctx, f.newCredential(t, "prod")))

	err := f.store.Save(ctx, f.newCredential(t, "prod"))
	require.True(t, credential.IsAlreadyExistsError(err), "want *AlreadyExistsError, got %T: %v", err, err)
}

func TestPostgres_Credential_List(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.ctx(t)
	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, f.store.Save(ctx, f.newCredential(t, name)))
	}
	got, err := f.store.List(ctx, f.orgID, f.projID)
	require.NoError(t, err)
	require.Len(t, got, 3)
}

// SECURITY: the cross-org case is the point of running as NOBYPASSRLS — the policy, not the WHERE clause, must filter.
func TestPostgres_Credential_AnotherOrgSeesZeroRows(t *testing.T) {
	f := newPGFixture(t)
	own := f.ctx(t)
	c := f.newCredential(t, "prod")
	require.NoError(t, f.store.Save(own, c))

	foreign := tenant.Into(t.Context(), tenant.Context{OrgID: f.otherOrg, ProjectID: f.otherPrj, UserID: f.userID})

	rows, err := f.store.List(foreign, f.orgID, f.projID)
	require.NoError(t, err)
	require.Empty(t, rows, "another org's scope must list zero rows")

	_, err = f.store.ByID(foreign, c.ID)
	require.True(t, credential.IsNotFoundError(err), "want *NotFoundError across orgs, got %T: %v", err, err)

	require.True(t, credential.IsNotFoundError(f.store.Delete(foreign, c.ID)),
		"another org must not be able to delete the row")

	still, err := f.store.ByID(own, c.ID)
	require.NoError(t, err, "the row must survive a foreign delete attempt")
	require.Equal(t, c.ID, still.ID)
}

func TestPostgres_Credential_WriteUnderAnotherOrgIsRejected(t *testing.T) {
	f := newPGFixture(t)
	foreign := tenant.Into(t.Context(), tenant.Context{OrgID: f.otherOrg, ProjectID: f.otherPrj, UserID: f.userID})

	// The aggregate names f.orgID while the scope names otherOrg, so the WITH CHECK clause must refuse it.
	err := f.store.Save(foreign, f.newCredential(t, "smuggled"))
	require.Error(t, err, "inserting a row whose org_id differs from the scope must fail")
	require.Contains(t, strings.ToLower(err.Error()), "row-level security")
}

func TestPostgres_Credential_Delete(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.ctx(t)
	c := f.newCredential(t, "prod")
	require.NoError(t, f.store.Save(ctx, c))
	require.NoError(t, f.store.Delete(ctx, c.ID))

	_, err := f.store.ByID(ctx, c.ID)
	require.True(t, credential.IsNotFoundError(err), "want *NotFoundError after delete, got %T: %v", err, err)
	require.True(t, credential.IsNotFoundError(f.store.Delete(ctx, c.ID)), "a second delete must be *NotFoundError")
}

// SECURITY: the spreadsheets foreign key is ON DELETE RESTRICT; the SQLSTATE must not leak as a pgx error.
func TestPostgres_Credential_DeleteReferencedIsInUse(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.ctx(t)
	c := f.newCredential(t, "prod")
	require.NoError(t, f.store.Save(ctx, c))

	now := time.Now().UTC()
	_, err := f.ownerDB.ExecContext(t.Context(),
		"INSERT INTO public."+f.prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, title, created_at, updated_at) "+
			"VALUES ($1, $2, $3, $4, 'FILE', 'Prices', $5, $5)",
		uuid.New(), f.orgID, f.projID, c.ID, now)
	require.NoError(t, err)

	delErr := f.store.Delete(ctx, c.ID)
	require.True(t, credential.IsInUseError(delErr), "want *InUseError, got %T: %v", delErr, delErr)
	require.NotContains(t, delErr.Error(), "23503", "the SQLSTATE must not leak into the domain error")

	got, err := f.store.ByID(ctx, c.ID)
	require.NoError(t, err, "a refused delete must leave the row in place")
	require.Equal(t, c.ID, got.ID)
}

func TestPostgres_Credential_RequiresTenantScope(t *testing.T) {
	f := newPGFixture(t)
	c := f.newCredential(t, "prod")

	require.True(t, tenant.IsMissingError(f.store.Save(t.Context(), c)))
	_, err := f.store.ByID(t.Context(), c.ID)
	require.True(t, tenant.IsMissingError(err))
	_, err = f.store.List(t.Context(), f.orgID, f.projID)
	require.True(t, tenant.IsMissingError(err))
	require.True(t, tenant.IsMissingError(f.store.Delete(t.Context(), c.ID)))

	unscoped := tenant.Into(t.Context(), tenant.Context{UserID: f.userID})
	_, err = f.store.ByID(unscoped, c.ID)
	require.True(t, tenant.IsUnscopedError(err), "want *UnscopedError, got %T: %v", err, err)
}

func TestPostgres_Credential_EnrollsInAnOuterUnitOfWork(t *testing.T) {
	f := newPGFixture(t)
	tc := tenant.Context{OrgID: f.orgID, ProjectID: f.projID, UserID: f.userID}
	c := f.newCredential(t, "in-tx")

	require.NoError(t, tenant.RunInTx(t.Context(), tenant.NewPgConn(f.appConn), tc, func(ctx context.Context) error {
		require.NoError(t, f.store.Save(ctx, c))
		got, err := f.store.ByID(ctx, c.ID)
		require.NoError(t, err)
		require.Equal(t, c.ID, got.ID)
		return nil
	}))

	got, err := f.store.ByID(f.ctx(t), c.ID)
	require.NoError(t, err, "the outer transaction must have committed the row")
	require.Equal(t, "in-tx", got.Name)
}
