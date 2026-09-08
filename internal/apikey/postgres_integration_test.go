//go:build integration

package apikey_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type pgProject struct {
	userID    uuid.UUID
	orgID     uuid.UUID
	projectID uuid.UUID
	sheetID   uuid.UUID
}

func (p pgProject) scope() tenant.Context {
	return tenant.Context{OrgID: p.orgID, ProjectID: p.projectID, UserID: p.userID}
}

func (p pgProject) ctx() context.Context {
	return tenant.Into(context.Background(), p.scope())
}

type pgFixture struct {
	store   apikey.Store
	svc     *apikey.Service
	sheets  *fakes.APIKeySheets
	appConn *sql.DB
	prefix  string
	unex    *int
	a       pgProject
	b       pgProject
}

// NOTE: pgtest reuses TEST_PG_DSN when set, so role and table names must be unique per run.
func pgSuffix(t *testing.T) string {
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

func newPGFixture(t *testing.T) *pgFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := pgSuffix(t)
	ownerRole := "opensheet_keyowner_" + suffix
	appRole := "opensheet_keyapp_" + suffix
	prefix := "k" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	createRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	// SECURITY: the app role must be NOBYPASSRLS or every isolation assertion below proves nothing.
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
	cfg.DB.Driver = "postgres"
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.DB.AllowBypassRLS = false
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	f := &pgFixture{prefix: prefix, sheets: fakes.NewAPIKeySheets()}
	f.a = seedPGProject(t, migDB, prefix, "a-"+suffix)
	f.b = seedPGProject(t, migDB, prefix, "b-"+suffix)
	for _, p := range []pgProject{f.a, f.b} {
		f.sheets.Add(p.orgID, p.projectID, p.sheetID)
	}

	f.appConn, err = db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 4,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.appConn.Close() })

	f.store = apikey.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix},
		db.Pool{W: f.appConn, R: f.appConn},
		tenant.NewPgConn(f.appConn),
	)
	calls := 0
	f.unex = &calls
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", msg, codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(cause)
	}
	f.svc = apikey.NewService(f.store, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, f.sheets)
	return f
}

func seedPGProject(t *testing.T, migDB *sql.DB, prefix, slug string) pgProject {
	t.Helper()
	p := pgProject{userID: uuid.New(), orgID: uuid.New(), projectID: uuid.New(), sheetID: uuid.New()}
	credID, docID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := migDB.ExecContext(t.Context(), q, args...)
		require.NoError(t, err, q)
	}
	exec("INSERT INTO public."+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
		p.userID, p.userID.String()+"@x.co", now)
	exec("INSERT INTO public."+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'Org',$3,$4,$4)",
		p.orgID, slug, p.userID, now)
	exec("INSERT INTO public."+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'web','Web',$3,$4,$4)",
		p.projectID, p.orgID, p.userID, now)
	exec("INSERT INTO public."+prefix+"credentials (id, org_id, project_id, name, kind, authorized_by_user_id, sealed, created_at, updated_at) "+
		"VALUES ($1,$2,$3,'cred','service_account',$4,'\\x00'::bytea,$5,$5)",
		credID, p.orgID, p.projectID, p.userID, now)
	exec("INSERT INTO public."+prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$6)",
		docID, p.orgID, p.projectID, credID, "gfile-"+slug, now)
	exec("INSERT INTO public."+prefix+"sheets (id, org_id, project_id, spreadsheet_id, slug, created_at, updated_at) VALUES ($1,$2,$3,$4,'rows',$5,$5)",
		p.sheetID, p.orgID, p.projectID, docID, now)
	return p
}

func (f *pgFixture) requireNoBypassRLS(t *testing.T) {
	t.Helper()
	var bypass bool
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the app role must not hold BYPASSRLS or these assertions prove nothing")
}

func TestPostgres_APIKey_SaveAndLookup(t *testing.T) {
	f := newPGFixture(t)
	f.requireNoBypassRLS(t)
	ctx := f.a.ctx()
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

	k, plaintext, err := f.svc.Create(ctx, apikey.CreateRequest{
		Name:      "deploy bot",
		Scopes:    []string{authn.ScopeSheetsRead, authn.ScopeCachePurge},
		SheetIDs:  []uuid.UUID{f.a.sheetID},
		ExpiresAt: &expires,
	})
	require.NoError(t, err)
	require.Zero(t, *f.unex)

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	require.Equal(t, "deploy bot", got.Name)
	require.Equal(t, []string{authn.ScopeSheetsRead, authn.ScopeCachePurge}, got.Scopes,
		"text[] must round-trip in order")
	require.Equal(t, []uuid.UUID{f.a.sheetID}, got.SheetIDs)
	require.NotNil(t, got.ExpiresAt)
	require.True(t, got.ExpiresAt.Equal(expires))
	require.Nil(t, got.RevokedAt)
	require.Nil(t, got.LastUsedAt)
	require.Empty(t, got.SecretHash, "ByID must read through PublicColumns")

	authed, err := f.svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	require.Equal(t, k.ID, authed.ID)
	require.Empty(t, authed.SecretHash, "Authenticate must not hand the digest to the request layer")
	require.True(t, authed.Allows(authn.ScopeSheetsRead, f.a.sheetID))
	require.False(t, authed.Allows(authn.ScopeSheetsRead, f.b.sheetID),
		"a grant must never span projects")
}

func TestPostgres_APIKey_EmptyScopeAndGrantRoundTrip(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.a.ctx()

	k, _, err := f.svc.Create(ctx, apikey.CreateRequest{Name: "wide", Scopes: []string{authn.ScopeSheetsRead}})
	require.NoError(t, err)

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	require.Equal(t, []string{authn.ScopeSheetsRead}, got.Scopes)
	require.Empty(t, got.SheetIDs, "no grant rows means every sheet in this project")
	require.True(t, got.Allows(authn.ScopeSheetsRead, f.a.sheetID))
}

func TestPostgres_APIKey_ListCarriesNoSecretMaterial(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.a.ctx()
	for i := range 3 {
		_, _, err := f.svc.Create(ctx, apikey.CreateRequest{
			Name: fmt.Sprintf("key-%d", i), Scopes: []string{authn.ScopeAPIKeysRead},
		})
		require.NoError(t, err)
	}

	got, err := f.svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, k := range got {
		require.Empty(t, k.SecretHash, "key %s carries secret material in a List result", k.ID)
		require.NotEmpty(t, k.KeyPrefix)
	}
}

func TestPostgres_APIKey_RevokePreservesTheSecretHash(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.a.ctx()
	k, plaintext, err := f.svc.Create(ctx, apikey.CreateRequest{Name: "ci", Scopes: []string{authn.ScopeSheetsRead}})
	require.NoError(t, err)
	prefix, secret, err := apikey.Parse(plaintext)
	require.NoError(t, err)

	_, err = f.svc.Revoke(ctx, k.ID)
	require.NoError(t, err)

	stored, err := f.store.ByPrefix(context.Background(), prefix)
	require.NoError(t, err)
	require.True(t, stored.Verify(secret), "Save of a key read without secret material blanked the stored hash")
	require.NotNil(t, stored.RevokedAt)
	require.False(t, stored.Active(time.Now().UTC()))

	_, err = f.svc.Authenticate(context.Background(), plaintext)
	require.True(t, apikey.IsUnauthorizedError(err), "want *UnauthorizedError, got %T: %v", err, err)
}

func TestPostgres_APIKey_DeleteCascadesTheGrant(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.a.ctx()
	k, _, err := f.svc.Create(ctx, apikey.CreateRequest{
		Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{f.a.sheetID},
	})
	require.NoError(t, err)

	require.NoError(t, f.svc.Delete(ctx, k.ID))
	_, err = f.store.ByID(ctx, k.ID)
	require.True(t, apikey.IsNotFoundError(err), "want *NotFoundError, got %T: %v", err, err)

	var grants int
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"api_key_sheets WHERE api_key_id = $1", k.ID).Scan(&grants))
	require.Zero(t, grants, "the grant rows must go with the key")

	require.True(t, apikey.IsNotFoundError(f.svc.Delete(ctx, k.ID)))
}

func TestPostgres_APIKey_CrossOrgReadsAreEmptyUnderRLS(t *testing.T) {
	f := newPGFixture(t)
	f.requireNoBypassRLS(t)

	kb, _, err := f.svc.Create(f.b.ctx(), apikey.CreateRequest{Name: "b key", Scopes: []string{authn.ScopeSheetsRead}})
	require.NoError(t, err)

	got, err := f.svc.List(f.a.ctx())
	require.NoError(t, err)
	require.Empty(t, got, "org A must not list org B's keys")

	_, err = f.store.ByID(f.a.ctx(), kb.ID)
	require.True(t, apikey.IsNotFoundError(err), "want *NotFoundError, got %T: %v", err, err)

	_, err = f.svc.Revoke(f.a.ctx(), kb.ID)
	require.True(t, apikey.IsNotFoundError(err), "want *NotFoundError, got %T: %v", err, err)

	require.True(t, apikey.IsNotFoundError(f.svc.Delete(f.a.ctx(), kb.ID)))

	stillThere, err := f.store.ByID(f.b.ctx(), kb.ID)
	require.NoError(t, err, "org A's attempts must not have touched org B's key")
	require.Nil(t, stillThere.RevokedAt)
}

// TestAPIKey_ByPrefixCrossesOrgsButServiceDoesNot pins the one deliberate RLS bypass in the module:
// ByPrefix must resolve a key's own org before any scope exists, and Authenticate must still refuse
// to act outside that org.
func TestAPIKey_ByPrefixCrossesOrgsButServiceDoesNot(t *testing.T) {
	f := newPGFixture(t)
	f.requireNoBypassRLS(t)

	kb, plaintext, err := f.svc.Create(f.b.ctx(), apikey.CreateRequest{
		Name: "b key", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{f.b.sheetID},
	})
	require.NoError(t, err)
	prefix, secret, err := apikey.Parse(plaintext)
	require.NoError(t, err)

	var direct int
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"api_keys").Scan(&direct))
	require.Zero(t, direct, "the app role must read no api_keys straight off the table under FORCE row level security")

	resolved, err := f.store.ByPrefix(context.Background(), prefix)
	require.NoError(t, err, "ByPrefix must not need a tenant scope the caller cannot have yet")
	require.Equal(t, kb.ID, resolved.ID)
	require.Equal(t, f.b.orgID, resolved.OrgID)
	require.Equal(t, f.b.projectID, resolved.ProjectID)
	require.True(t, resolved.Verify(secret), "ByPrefix is the only read that may return secret material")
	require.Equal(t, []uuid.UUID{f.b.sheetID}, resolved.SheetIDs,
		"the grant must be read under the key's own org; a filtered-out grant would silently widen the key")

	authed, err := f.svc.Authenticate(context.Background(), plaintext)
	require.NoError(t, err)
	require.Equal(t, f.b.orgID, authed.OrgID)
	require.False(t, authed.Allows(authn.ScopeSheetsRead, f.a.sheetID), "a key must not reach another project's sheet")

	_, err = f.svc.Authenticate(f.a.ctx(), plaintext)
	require.True(t, apikey.IsUnauthorizedError(err),
		"Authenticate under org A's scope must refuse org B's key; got %T: %v", err, err)
	require.Zero(t, *f.unex, "a refusal is not an infrastructure failure")
}

func TestPostgres_APIKey_DuplicateKeyPrefixIsRejected(t *testing.T) {
	f := newPGFixture(t)
	ctx := f.a.ctx()

	first, _, err := f.svc.Create(ctx, apikey.CreateRequest{Name: "first", Scopes: []string{authn.ScopeSheetsRead}})
	require.NoError(t, err)

	clash, _ := mintFor(t, f.a.scope(), []string{authn.ScopeSheetsRead}, nil, nil)
	clash.KeyPrefix = first.KeyPrefix
	err = f.store.Save(ctx, clash)
	require.True(t, apikey.IsAlreadyExistsError(err), "want *AlreadyExistsError, got %T: %v", err, err)
}

func TestPostgres_APIKey_TenantScopeIsRequired(t *testing.T) {
	f := newPGFixture(t)
	k, _ := mintFor(t, f.a.scope(), []string{authn.ScopeSheetsRead}, nil, nil)
	bare := context.Background()

	require.True(t, tenant.IsMissingError(f.store.Save(bare, k)))
	_, err := f.store.ByID(bare, k.ID)
	require.True(t, tenant.IsMissingError(err))
	_, err = f.store.List(bare, f.a.orgID, f.a.projectID)
	require.True(t, tenant.IsMissingError(err))
	require.True(t, tenant.IsMissingError(f.store.Delete(bare, k.ID)))
}
