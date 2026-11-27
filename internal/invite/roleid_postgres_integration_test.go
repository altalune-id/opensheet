//go:build integration

package invite_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestPostgres_Invite_RoleIDRoundTrip(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	prefix := cfg.DB.TablePrefix
	userID, orgID := seedInviteUserAndOrg(t, sqlDB, prefix)
	roleID := uuid.New()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"roles (id, org_id, name, permissions, created_at, updated_at) VALUES ($1, $2, 'Clerk', '{}', now(), now())", roleID, orgID)
	require.NoError(t, err)
	store := invite.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, tenant.NewPgConn(sqlDB))
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	inv, err := invite.New(invite.NewParams{OrgID: orgID, Email: "c@x.co", Role: invite.RoleCustom, RoleID: roleID, TTL: time.Hour, Token: "raw-custom"})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, inv))

	got, err := store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, roleID, got.RoleID)
	got, err = store.ByTokenHash(t.Context(), inv.TokenHash)
	require.NoError(t, err)
	assert.Equal(t, roleID, got.RoleID, "the definer resolver must carry role_id")
	pending, err := store.FindPendingForEmail(t.Context(), "c@x.co")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, roleID, pending[0].RoleID, "the pending-for-email resolver must carry role_id")

	inv.Role, inv.RoleID = invite.RoleMember, uuid.Nil
	require.NoError(t, store.Save(ctx, inv))
	got, err = store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, got.RoleID, "a built-in role clears role_id")
}
