package role_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/schema"
)

func sqliteFixture(t *testing.T) storeFixture {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)
	cfg := config.Defaults()
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	prefix := cfg.DB.TablePrefix
	now := sqliteent.SQLiteTime(time.Now())
	userID := uuid.New()
	_, err = sqlDB.Exec("INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
		userID.String(), userID.String()+"@x.co", now, now)
	require.NoError(t, err)
	org := func() tenant.Context {
		id := uuid.New()
		_, err := sqlDB.Exec("INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, 'Org', ?, ?, ?)",
			id.String(), id.String()[:8], userID.String(), now, now)
		require.NoError(t, err)
		return tenant.Context{OrgID: id, UserID: userID}
	}
	return storeFixture{
		store: role.NewStore(db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, nil),
		org:   org(), other: org(),
		hold: func(t *testing.T, orgID, roleID uuid.UUID) {
			t.Helper()
			_, err := sqlDB.Exec("INSERT INTO "+prefix+"memberships (id, org_id, user_id, role, role_id, system, created_at) VALUES (?, ?, ?, 'custom', ?, 0, ?)",
				uuid.NewString(), orgID.String(), userID.String(), roleID.String(), now)
			require.NoError(t, err)
		},
		release: func(t *testing.T, orgID, roleID uuid.UUID) {
			t.Helper()
			_, err := sqlDB.Exec("UPDATE "+prefix+"memberships SET role = 'member', role_id = NULL WHERE org_id = ? AND role_id = ?", orgID.String(), roleID.String())
			require.NoError(t, err)
		},
		invite: func(t *testing.T, orgID, roleID uuid.UUID, accepted bool) {
			t.Helper()
			var acceptedAt any
			if accepted {
				acceptedAt = now
			}
			_, err := sqlDB.Exec("INSERT INTO "+prefix+"invites (id, org_id, email, role, role_id, token_hash, expires_at, accepted_at, invited_by, created_at) VALUES (?, ?, 'n@x.co', 'custom', ?, ?, ?, ?, ?, ?)",
				uuid.NewString(), orgID.String(), roleID.String(), uuid.NewString(), now, acceptedAt, userID.String(), now)
			require.NoError(t, err)
		},
	}
}

func fakeFixture(t *testing.T) storeFixture {
	t.Helper()
	f := fakes.NewRole()
	return storeFixture{
		store: f,
		org:   tenant.Context{OrgID: uuid.New(), UserID: uuid.New()},
		other: tenant.Context{OrgID: uuid.New(), UserID: uuid.New()},
		hold:  func(_ *testing.T, _, roleID uuid.UUID) { f.Hold(roleID) },
	}
}

func TestRoleStore_RoundTrip(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { roundTrip(t, sqliteFixture(t)) })
	t.Run("fake", func(t *testing.T) { roundTrip(t, fakeFixture(t)) })
}

func TestRoleStore_NameUniqueIgnoringCase(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { nameUniqueIgnoringCase(t, sqliteFixture(t)) })
	t.Run("fake", func(t *testing.T) { nameUniqueIgnoringCase(t, fakeFixture(t)) })
}

func TestRoleStore_SaveIfVersion(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { saveIfVersion(t, sqliteFixture(t)) })
	t.Run("fake", func(t *testing.T) { saveIfVersion(t, fakeFixture(t)) })
}

func TestRoleStore_OtherOrgInvisible(t *testing.T) {
	otherOrgInvisible(t, sqliteFixture(t))
}

func TestRoleStore_DeleteHeldIsInUse(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { deleteHeldIsInUse(t, sqliteFixture(t)) })
	t.Run("fake", func(t *testing.T) { deleteHeldIsInUse(t, fakeFixture(t)) })
}

func TestRoleStore_DeleteClearsAcceptedInvites(t *testing.T) {
	deleteClearsAcceptedInvites(t, sqliteFixture(t))
}

func TestRoleStore_DeletePendingInviteIsInUse(t *testing.T) {
	deletePendingInviteIsInUse(t, sqliteFixture(t))
}

func TestRoleStore_HeldByIsPerOrg(t *testing.T) {
	heldByIsPerOrg(t, sqliteFixture(t))
}
