//go:build integration

package role_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

type storeCase struct {
	name string
	run  func(t *testing.T, f storeFixture)
}

func runCases(t *testing.T, newFixture func(t *testing.T) storeFixture, cases ...storeCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newFixture(t)) })
	}
}

func postgresFixture(t *testing.T) storeFixture {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	prefix := h.Schema + "." + cfg.DB.TablePrefix
	now := time.Now().UTC()
	userID := uuid.New()
	_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@x.co", now)
	require.NoError(t, err)
	org := func() tenant.Context {
		id := uuid.New()
		_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Org', $3, $4, $4)",
			id, id.String()[:8], userID, now)
		require.NoError(t, err)
		return tenant.Context{OrgID: id, UserID: userID}
	}
	return storeFixture{
		store: role.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix}, db.Pool{W: sqlDB, R: sqlDB}, tenant.NewPgConn(sqlDB)),
		org:   org(), other: org(),
		hold: func(t *testing.T, orgID, roleID uuid.UUID) {
			t.Helper()
			_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"memberships (id, org_id, user_id, role, role_id, created_at) VALUES ($1, $2, $3, 'custom', $4, $5)",
				uuid.New(), orgID, userID, roleID, now)
			require.NoError(t, err)
		},
		release: func(t *testing.T, orgID, roleID uuid.UUID) {
			t.Helper()
			_, err := sqlDB.ExecContext(t.Context(), "UPDATE "+prefix+"memberships SET role = 'member', role_id = NULL WHERE org_id = $1 AND role_id = $2", orgID, roleID)
			require.NoError(t, err)
		},
		invite: func(t *testing.T, orgID, roleID uuid.UUID, accepted bool) {
			t.Helper()
			var acceptedAt *time.Time
			if accepted {
				acceptedAt = &now
			}
			_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"invites (id, org_id, email, role, role_id, token_hash, expires_at, accepted_at, invited_by, created_at) VALUES ($1, $2, 'n@x.co', 'custom', $3, $4, $5, $6, $7, $5)",
				uuid.New(), orgID, roleID, uuid.NewString(), now, acceptedAt, userID)
			require.NoError(t, err)
		},
	}
}

func TestPostgresRoleStore_Contract(t *testing.T) {
	runCases(t, postgresFixture,
		storeCase{"round trip", roundTrip},
		storeCase{"name unique ignoring case", nameUniqueIgnoringCase},
		storeCase{"save if version", saveIfVersion},
		storeCase{"other org invisible", otherOrgInvisible},
		storeCase{"delete held is in use", deleteHeldIsInUse},
		storeCase{"held by is per org", heldByIsPerOrg},
		storeCase{"delete clears accepted invites", deleteClearsAcceptedInvites},
		storeCase{"delete pending invite is in use", deletePendingInviteIsInUse},
	)
}
