//go:build integration

package invite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/nanoid"
	"altalune.id/template/schema"
)

// NOTE: pgtest reuses TEST_PG_DSN when set, so role and table names must be unique per run.
func uniqueInviteSuffix(t *testing.T) string {
	t.Helper()
	s, err := nanoid.New(10)
	require.NoError(t, err)
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func createInviteRole(t *testing.T, admin *sql.DB, name, attrs string) {
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

type inviteDefinerFixture struct {
	store   invite.Store
	appConn *sql.DB
	prefix  string
	userID  uuid.UUID
	orgID   uuid.UUID
	inv     *invite.Invite
	token   string
}

// newInviteDefinerFixture migrates under an owner role and returns a store bound to a NOBYPASSRLS
// app role on a single connection, so a poisoned GUC survives into the next query.
func newInviteDefinerFixture(t *testing.T) *inviteDefinerFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueInviteSuffix(t)
	ownerRole := "altempl_invowner_" + suffix
	appRole := "altempl_invapp_" + suffix
	prefix := "t" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	createInviteRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	// NOTE: USAGE as well as CREATE — without USAGE the schema drops out of search_path and DDL fails with 3F000.
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	createInviteRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)

	// NOTE: mirrors scripts/db/bootstrap.template.sql — the service role's EXECUTE comes from default
	// privileges granted at creation time, which migration 006's REVOKE ... FROM PUBLIC does not touch.
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

	f := &inviteDefinerFixture{
		prefix: prefix,
		userID: uuid.New(),
		orgID:  uuid.New(),
		token:  "tok-" + suffix,
	}
	now := time.Now().UTC().Truncate(time.Microsecond)

	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
		f.userID, f.userID.String()+"@x.co", now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"orgs (id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,'Acme',false,$3,$4,$4)",
		f.orgID, "acme-"+suffix, f.userID, now)
	require.NoError(t, err)

	f.inv = newInvite(t, f.orgID, "newcomer-"+suffix+"@example.com", f.token)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"invites (id, org_id, email, role, token_hash, expires_at, accepted_at, invited_by, created_at) VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$8)",
		f.inv.ID, f.inv.OrgID, f.inv.Email, string(f.inv.Role), f.inv.TokenHash, f.inv.ExpiresAt, f.userID, f.inv.CreatedAt)
	require.NoError(t, err)

	// MaxOpenConns 1 is load-bearing: the poisoned connection must be the one the next query reuses.
	f.appConn, err = db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.appConn.Close() })

	f.store = invite.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix},
		db.Pool{W: f.appConn, R: f.appConn},
		tenant.NewPgConn(f.appConn),
	)
	return f
}

func (f *inviteDefinerFixture) requireNoBypassRLS(t *testing.T) {
	t.Helper()
	var bypass bool
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the app role must not hold BYPASSRLS or the reads below prove nothing")
}

func (f *inviteDefinerFixture) requireMatches(t *testing.T, got *invite.Invite) {
	t.Helper()
	require.Equal(t, f.inv.ID, got.ID)
	require.Equal(t, f.orgID, got.OrgID, "org_id must survive the wrapper projection")
	require.Equal(t, f.inv.Email, got.Email)
	require.Equal(t, f.inv.Role, got.Role)
	require.Equal(t, f.inv.TokenHash, got.TokenHash)
	require.WithinDuration(t, f.inv.ExpiresAt, got.ExpiresAt, time.Second)
	require.Nil(t, got.UsedAt, "accepted_at was NULL and must map to a nil UsedAt")
	require.WithinDuration(t, f.inv.CreatedAt, got.CreatedAt, time.Second)
}

// poisonTenantGUC runs a tenant-scoped transaction to completion. A transaction-local set_config
// resets to ” rather than NULL, which is what made ”::uuid throw 22P02 on the next read.
func (f *inviteDefinerFixture) poisonTenantGUC(t *testing.T) {
	t.Helper()
	pc := tenant.NewPgConn(f.appConn)
	tx, err := pc.BeginTenanted(t.Context(), tenant.Context{OrgID: f.orgID, UserID: f.userID})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var raw sql.NullString
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		`SELECT current_setting('app.current_org_id', true)`).Scan(&raw))
	require.True(t, raw.Valid, "test premise: the GUC must be set, not NULL")
	require.Empty(t, raw.String, "test premise: a finished tenant transaction must leave the GUC empty")
}

func TestPostgres_InviteWrappers_ReadAfterPoisonedTenantGUC(t *testing.T) {
	f := newInviteDefinerFixture(t)
	f.requireNoBypassRLS(t)
	f.poisonTenantGUC(t)

	var direct int
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"invites").Scan(&direct))
	require.Zero(t, direct, "app role must read no invites from the table under FORCE row level security")

	pending, err := f.store.FindPendingForEmail(t.Context(), f.inv.Email)
	require.NoError(t, err, "an empty app.current_org_id must not blow up the RLS policy with 22P02")
	require.Len(t, pending, 1, "the wrapper must lift RLS for a NOBYPASSRLS caller")
	f.requireMatches(t, pending[0])

	byHash, err := f.store.ByTokenHash(t.Context(), invite.HashToken(f.token))
	require.NoError(t, err)
	f.requireMatches(t, byHash)
}

func TestPostgres_InviteWrappers_ReadWithoutTenantScope(t *testing.T) {
	f := newInviteDefinerFixture(t)
	f.requireNoBypassRLS(t)

	pending, err := f.store.FindPendingForEmail(t.Context(), f.inv.Email)
	require.NoError(t, err, "signup runs before any tenant scope exists")
	require.Len(t, pending, 1)
	f.requireMatches(t, pending[0])

	byHash, err := f.store.ByTokenHash(t.Context(), invite.HashToken(f.token))
	require.NoError(t, err)
	f.requireMatches(t, byHash)
}

func TestPostgres_InviteWrappers_MissingTokenIsNotFound(t *testing.T) {
	f := newInviteDefinerFixture(t)
	f.poisonTenantGUC(t)

	_, err := f.store.ByTokenHash(t.Context(), invite.HashToken("no-such-token"))
	require.True(t, invite.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)

	pending, err := f.store.FindPendingForEmail(t.Context(), "nobody@example.com")
	require.NoError(t, err)
	require.Empty(t, pending)
}
