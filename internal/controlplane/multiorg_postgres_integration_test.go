//go:build integration

package controlplane_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

type pgMultiOrg struct {
	h                                  *osHarness
	orgSvc                             *org.Service
	userID                             uuid.UUID
	firstID, secondID, foreignID       uuid.UUID
	firstProj, secondProj, foreignProj uuid.UUID
}

func createRole(t *testing.T, admin *sql.DB, name, attrs string) {
	t.Helper()
	_, err := admin.ExecContext(t.Context(), fmt.Sprintf(`CREATE ROLE %q %s`, name, attrs))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %q`, name))
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %q`, name))
	})
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT %q TO CURRENT_USER`, name))
	require.NoError(t, err)
}

// SECURITY: the stores run as a NOBYPASSRLS role on a FORCE RLS schema, so the org definer, per-org project reads and membership checks are what admit the person.
func newPGMultiOrg(t *testing.T, principal func(f *pgMultiOrg) session.Principal) *pgMultiOrg {
	t.Helper()
	h := pgtest.New(t)
	raw, err := nanoid.New(10)
	require.NoError(t, err)
	suffix := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(raw))
	ownerRole, appRole, prefix := "opensheet_cpowner_"+suffix, "opensheet_cpapp_"+suffix, "t"+suffix+"_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	createRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	createRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	for _, stmt := range []string{
		`GRANT USAGE, CREATE ON SCHEMA public TO %[1]q`,
		`GRANT USAGE ON SCHEMA public TO %[2]q`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[2]q`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %[2]q`,
	} {
		_, err = admin.ExecContext(t.Context(), fmt.Sprintf(stmt, ownerRole, appRole))
		require.NoError(t, err)
	}

	migDB, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migDB.Close() })
	cfg := config.Defaults()
	cfg.DB.Driver, cfg.DB.Schema, cfg.DB.TablePrefix = "postgres", "public", prefix
	cfg.DB.AllowBypassRLS, cfg.Tenant.RLSEnforce = false, true
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	f := &pgMultiOrg{userID: uuid.New(), firstID: uuid.New(), secondID: uuid.New(), foreignID: uuid.New()}
	now := time.Now().UTC()
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := migDB.ExecContext(t.Context(), strings.ReplaceAll(q, "P_", "public."+prefix), args...)
		require.NoError(t, err)
	}
	stranger := uuid.New()
	for _, u := range []uuid.UUID{f.userID, stranger} {
		exec("INSERT INTO P_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
			u, u.String()+"@x.co", now)
	}
	seed := func(orgID uuid.UUID, slug string, member uuid.UUID) uuid.UUID {
		exec("INSERT INTO P_orgs (id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,$2,false,$3,$4,$4)",
			orgID, slug, member, now)
		exec("INSERT INTO P_memberships (id, org_id, user_id, role, system, created_at) VALUES ($1,$2,$3,'member',false,$4)",
			uuid.New(), orgID, member, now)
		projID := uuid.New()
		exec("INSERT INTO P_projects (id, org_id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,$3,$3,false,$4,$5,$5)",
			projID, orgID, slug+"-project", member, now)
		return projID
	}
	f.firstProj = seed(f.firstID, "first-org", f.userID)
	f.secondProj = seed(f.secondID, "second-org", f.userID)
	f.foreignProj = seed(f.foreignID, "foreign-org", stranger)

	appConn, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 2,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appConn.Close() })
	var bypass bool
	require.NoError(t, appConn.QueryRowContext(t.Context(), `SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the app role must not bypass RLS or these tests prove nothing")

	dbCfg := db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix}
	pool, pc := db.Pool{W: appConn, R: appConn}, tenant.NewPgConn(appConn)
	orgs := org.NewStore(dbCfg, pool, pc)
	f.h = buildOSHarnessOn(t, principal(f), nil, false, harnessStores{
		orgs: orgs, projs: project.NewStore(dbCfg, pool, pc), creds: credential.NewStore(dbCfg, pool, pc),
	})
	log := slog.New(slog.DiscardHandler)
	f.orgSvc = org.NewService(orgs, capabilities.Capabilities{}, log, apperror.NewReporter(log, false).Unexpected, tenant.NewUnitOfWork(dbCfg, pool, pc))
	return f
}

func TestPostgres_ListProjects_TokenInTwoOrgsListsBoth(t *testing.T) {
	f := newPGMultiOrg(t, func(f *pgMultiOrg) session.Principal { return multiOrgToken(f.userID) })

	require.Equal(t, map[string]string{
		"first-org-project":  "first-org",
		"second-org-project": "second-org",
	}, (&multiOrgFixture{h: f.h}).listProjects(t), "under RLS a token in two orgs lists both orgs' projects and nothing else")
}

func TestPostgres_ScopeToProject_TokenResolvesSecondOrg(t *testing.T) {
	f := newPGMultiOrg(t, func(f *pgMultiOrg) session.Principal {
		p := multiOrgToken(f.userID)
		p.ActiveOrgID = f.firstID
		return p
	})
	mf := &multiOrgFixture{h: f.h}

	require.NoError(t, mf.listCredentials(f.secondProj), "under RLS a member must reach a project in their second org")
	err := mf.listCredentials(f.foreignProj)
	require.Equal(t, connect.CodeNotFound, connectCode(err), "under RLS a project outside every membership reads as absent")

	ctx := context.Background()
	require.NoError(t, f.orgSvc.RequireMember(tenant.WithOrg(ctx, f.secondID), f.secondID, f.userID))
	err = f.orgSvc.RequireMember(tenant.WithOrg(ctx, f.foreignID), f.foreignID, f.userID)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok, "err = %v", err)
	require.Equal(t, apperror.CodeOrgMembershipMissing, ae.Code(), "a non-member must be refused, not failed, under RLS")
}
