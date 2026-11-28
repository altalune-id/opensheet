//go:build integration

package org_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestPostgres_MembershipGrantRoundTrip(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	prefix := cfg.DB.TablePrefix
	store := org.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, tenant.NewPgConn(sqlDB))

	userID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)", userID, userID.String()+"@x.co", now)
	require.NoError(t, err)
	o, err := org.NewOrg("acme", "Acme", userID)
	require.NoError(t, err)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: o.ID, UserID: userID})
	require.NoError(t, store.Save(ctx, o))
	p1, p2 := uuid.New(), uuid.New()
	_, err = sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $3, 'p1', 'P', $4, $5, $5), ($2, $3, 'p2', 'P', $4, $5, $5)", p1, p2, o.ID, userID, now)
	require.NoError(t, err)

	require.NoError(t, store.SaveMembership(ctx, &org.Membership{OrgID: o.ID, UserID: userID, Role: org.RoleMember, CreatedAt: now}))
	got, err := store.MembershipOf(ctx, o.ID, userID)
	require.NoError(t, err)
	assert.True(t, got.AllProjects, "a new membership takes the all-projects default")

	require.NoError(t, store.SetProjectGrant(ctx, o.ID, userID, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1, p2}}))
	got, err = store.MembershipOf(ctx, o.ID, userID)
	require.NoError(t, err)
	assert.False(t, got.AllProjects)
	assert.ElementsMatch(t, []uuid.UUID{p1, p2}, got.ProjectIDs)
	members, err := store.ListMembers(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.ElementsMatch(t, []uuid.UUID{p1, p2}, members[0].ProjectIDs)
	profiles, err := store.ListMemberProfiles(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.ElementsMatch(t, []uuid.UUID{p1, p2}, profiles[0].ProjectIDs)

	got.Role = org.RoleAdmin
	require.NoError(t, store.SaveMembership(ctx, got))
	require.NoError(t, store.SetProjectGrant(ctx, o.ID, userID, org.ProjectGrant{ProjectIDs: []uuid.UUID{p2}}))
	got, err = store.MembershipOf(ctx, o.ID, userID)
	require.NoError(t, err)
	assert.Equal(t, org.RoleAdmin, got.Role)
	assert.Equal(t, []uuid.UUID{p2}, got.ProjectIDs)

	_, err = sqlDB.ExecContext(t.Context(), "DELETE FROM "+prefix+"projects WHERE id = $1", p2)
	require.NoError(t, err)
	got, err = store.MembershipOf(ctx, o.ID, userID)
	require.NoError(t, err, "a restricted membership whose last project is gone still reads")
	assert.Empty(t, got.ProjectIDs)

	got.Role = org.RoleOwner
	require.Error(t, store.SaveMembership(ctx, got), "the owner CHECK refuses promoting a restricted member")
	require.NoError(t, store.SetProjectGrant(ctx, o.ID, userID, org.ProjectGrant{All: true}))
	require.NoError(t, store.SaveMembership(ctx, got))
	got, err = store.MembershipOf(ctx, o.ID, userID)
	require.NoError(t, err)
	assert.True(t, got.AllProjects)
	assert.Empty(t, got.ProjectIDs)

	err = store.SetProjectGrant(ctx, o.ID, uuid.New(), org.ProjectGrant{All: true})
	assert.True(t, org.IsMembershipMissingError(err), "%T %v", err, err)
}
