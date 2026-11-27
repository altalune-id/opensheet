package boot

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/schema"
)

type joinFixture struct {
	cfg     *config.Config
	sqlDB   *sql.DB
	prefix  string
	now     string
	orgID   uuid.UUID
	ownerID uuid.UUID
	users   user.Store
	orgs    org.Store
	projs   project.Store
	invs    invite.Store
	uow     tenant.UnitOfWork
	log     *slog.Logger
	unex    apperror.UnexpectedFunc
}

func newJoinFixture(t *testing.T, mode config.Mode) *joinFixture {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)
	cfg := config.Defaults()
	cfg.Mode = mode
	cfg.DB.Driver = db.DriverSQLite
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	pool := db.Pool{W: sqlDB, R: sqlDB}
	log := slog.New(slog.DiscardHandler)
	f := &joinFixture{
		cfg: cfg, sqlDB: sqlDB, prefix: cfg.DB.TablePrefix, now: sqliteent.SQLiteTime(time.Now()),
		orgID: uuid.New(), ownerID: uuid.New(),
		users: user.NewStore(cfg.DB, pool), orgs: org.NewStore(cfg.DB, pool, nil),
		projs: project.NewStore(cfg.DB, pool, nil), invs: invite.NewStore(cfg.DB, pool, nil),
		uow: tenant.NewUnitOfWork(cfg.DB, pool, nil), log: log, unex: apperror.NewReporter(log, false).Unexpected,
	}
	f.exec(t, "INSERT INTO "+f.prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, 'owner@example.com', 'Owner', '', 0, ?, ?)", f.ownerID.String(), f.now, f.now)
	f.exec(t, "INSERT INTO "+f.prefix+"orgs (id, slug, name, created_by, created_at, updated_at, system) VALUES (?, 'acme', 'Acme', ?, ?, ?, ?)",
		f.orgID.String(), f.ownerID.String(), f.now, f.now, mode == config.ModeSelfhosted)
	f.exec(t, "INSERT INTO "+f.prefix+"memberships (id, org_id, user_id, role, role_id, system, created_at) VALUES (?, ?, ?, 'owner', NULL, 1, ?)",
		uuid.NewString(), f.orgID.String(), f.ownerID.String(), f.now)
	return f
}

func (f *joinFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, err := f.sqlDB.Exec(q, args...)
	require.NoError(t, err)
}

func (f *joinFixture) role(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.exec(t, "INSERT INTO "+f.prefix+"roles (id, org_id, name, description, permissions, created_at, updated_at, version) VALUES (?, ?, 'Clerk', '', '[]', ?, ?, 1)",
		id.String(), f.orgID.String(), f.now, f.now)
	return id
}

func (f *joinFixture) invite(t *testing.T, email, role string, roleID uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	id, raw := uuid.New(), "raw-"+uuid.NewString()
	var rid any
	if roleID != uuid.Nil {
		rid = roleID.String()
	}
	f.exec(t, "INSERT INTO "+f.prefix+"invites (id, org_id, email, role, role_id, token_hash, expires_at, accepted_at, invited_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)",
		id.String(), f.orgID.String(), email, role, rid, invite.HashToken(raw), sqliteent.SQLiteTime(time.Now().Add(time.Hour)), f.ownerID.String(), f.now)
	return id, raw
}

func (f *joinFixture) failOn(t *testing.T, table, event string) {
	t.Helper()
	f.exec(t, "CREATE TRIGGER fail_"+table+" BEFORE "+event+" ON "+f.prefix+table+" BEGIN SELECT RAISE(ABORT, 'injected'); END")
}

func (f *joinFixture) used(t *testing.T, inviteID uuid.UUID) bool {
	t.Helper()
	var accepted sql.NullString
	require.NoError(t, f.sqlDB.QueryRow("SELECT accepted_at FROM "+f.prefix+"invites WHERE id = ?", inviteID.String()).Scan(&accepted))
	return accepted.Valid
}

type memberRow struct {
	role   string
	roleID sql.NullString
	system bool
}

func (f *joinFixture) member(t *testing.T, email string) (memberRow, bool) {
	t.Helper()
	var m memberRow
	err := f.sqlDB.QueryRow("SELECT m.role, m.role_id, m.system FROM "+f.prefix+"memberships m JOIN "+f.prefix+"users u ON u.id = m.user_id WHERE m.org_id = ? AND u.email = ?",
		f.orgID.String(), email).Scan(&m.role, &m.roleID, &m.system)
	if err == sql.ErrNoRows {
		return m, false
	}
	require.NoError(t, err)
	return m, true
}

func (f *joinFixture) onboard() *user.OnboardWorkflow {
	return newOnboardWorkflow(f.cfg, f.users, f.orgs, f.projs, f.invs, f.uow, f.log, f.unex)
}

func (f *joinFixture) accept() *invite.AcceptWorkflow {
	return newAcceptWorkflow(f.invs, f.users, f.orgs, f.uow, f.log, f.unex)
}

func (f *joinFixture) signIn(t *testing.T, email string) (uuid.UUID, error) {
	t.Helper()
	id := uuid.New()
	f.exec(t, "INSERT INTO "+f.prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, 'New', '', 0, ?, ?)", id.String(), email, f.now, f.now)
	_, err := f.onboard().Onboard(t.Context(), id, email)
	return id, err
}

func joinModes() []config.Mode { return []config.Mode{config.ModeSelfhosted, config.ModeCloud} }

func TestOnboard_CustomRoleInviteGivesRoleID(t *testing.T) {
	for _, mode := range joinModes() {
		t.Run(string(mode), func(t *testing.T) {
			f := newJoinFixture(t, mode)
			roleID := f.role(t)
			inviteID, _ := f.invite(t, "cara@example.com", "custom", roleID)

			_, err := f.signIn(t, "cara@example.com")
			require.NoError(t, err)
			m, ok := f.member(t, "cara@example.com")
			require.True(t, ok)
			require.Equal(t, "custom", m.role)
			require.Equal(t, roleID.String(), m.roleID.String)
			require.True(t, f.used(t, inviteID))
		})
	}
}

func TestOnboard_FailedMembershipLeavesInviteUnused(t *testing.T) {
	for _, mode := range joinModes() {
		t.Run(string(mode), func(t *testing.T) {
			f := newJoinFixture(t, mode)
			inviteID, _ := f.invite(t, "dan@example.com", "member", uuid.Nil)
			f.failOn(t, "memberships", "INSERT")

			_, err := f.signIn(t, "dan@example.com")
			require.Error(t, err)
			require.False(t, f.used(t, inviteID), "the invite stays unused when the membership is not written")
		})
	}
}

func TestAccept_FailedInviteSaveLeavesNoMembership(t *testing.T) {
	f := newJoinFixture(t, config.ModeCloud)
	_, raw := f.invite(t, "erin@example.com", "member", uuid.Nil)
	f.failOn(t, "invites", "UPDATE")

	_, err := f.accept().Execute(t.Context(), invite.AcceptRequest{Token: raw, Email: "erin@example.com"})
	require.Error(t, err)
	_, ok := f.member(t, "erin@example.com")
	require.False(t, ok, "Accept writes the membership and the consumed invite together")
}

func TestAccept_ExistingOwnerKeepsRoleAndSystem(t *testing.T) {
	f := newJoinFixture(t, config.ModeCloud)
	inviteID, raw := f.invite(t, "owner@example.com", "member", uuid.Nil)

	_, err := f.accept().Execute(t.Context(), invite.AcceptRequest{Token: raw, Email: "owner@example.com"})
	require.NoError(t, err)
	m, ok := f.member(t, "owner@example.com")
	require.True(t, ok)
	require.Equal(t, memberRow{role: "owner", system: true}, m)
	require.True(t, f.used(t, inviteID))
}

type staleMemberships struct{ orgStoreForOnboard }

func (staleMemberships) ListForUser(context.Context, uuid.UUID) ([]*user.OrgRef, error) {
	return nil, nil
}

func TestOnboard_ExistingOwnerKeepsRoleAndSystem(t *testing.T) {
	f := newJoinFixture(t, config.ModeCloud)
	inviteID, _ := f.invite(t, "owner@example.com", "member", uuid.Nil)
	wf := user.NewOnboardWorkflow(f.users, staleMemberships{orgStoreForOnboard{store: f.orgs}}, projectStoreForOnboard{store: f.projs},
		inviteStoreForOnboard{store: f.invs}, onboardPolicyFrom(f.cfg), f.log, f.unex, user.WithOnboardUnitOfWork(f.uow))

	_, err := wf.Onboard(t.Context(), f.ownerID, "owner@example.com")
	require.NoError(t, err)
	m, ok := f.member(t, "owner@example.com")
	require.True(t, ok)
	require.Equal(t, memberRow{role: "owner", system: true}, m)
	require.True(t, f.used(t, inviteID))
}
