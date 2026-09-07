//go:build integration

package org_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/schema"
)

func newPostgresStoreForTest(t *testing.T) (store org.Store, tc tenant.Context, prefix string) { //nolint:nonamedreturns // three heterogeneous returns read cleaner named
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true

	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	pc := tenant.NewPgConn(sqlDB)
	prefix = cfg.DB.TablePrefix
	store = org.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, pc)

	ownerID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		ownerID, ownerID.String()+"@x.co", now,
	)
	require.NoError(t, err)

	// NOTE: OrgID is left for each test to fill in with the org it operates on — the store derives its
	// RLS scope from the context, so a placeholder here would only prove that the superuser bypasses RLS.
	tc = tenant.Context{UserID: ownerID}
	return store, tc, prefix
}

// scopedTo returns a context naming orgID, the way org.Create and the handlers do in production.
func scopedTo(t *testing.T, tc tenant.Context, orgID uuid.UUID) context.Context {
	t.Helper()
	tc.OrgID = orgID
	return tenant.Into(t.Context(), tc)
}

func TestPostgres_SaveAndLookup(t *testing.T) {
	store, tc, _ := newPostgresStoreForTest(t)

	o, err := org.NewOrg("acme", "Acme", tc.UserID)
	require.NoError(t, err)
	ctx := scopedTo(t, tc, o.ID)
	require.NoError(t, store.Save(ctx, o))

	got, err := store.BySlug(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, o.ID, got.ID)
	assert.Equal(t, "Acme", got.Name)
	assert.Equal(t, tc.UserID, got.OwnerID)

	got2, err := store.ByID(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme", got2.Slug)
}

func TestPostgres_NotFound(t *testing.T) {
	store, tc, _ := newPostgresStoreForTest(t)
	ctx := scopedTo(t, tc, uuid.New())

	_, err := store.ByID(ctx, uuid.New())
	assert.True(t, org.IsNotFoundError(err), "ByID: want NotFoundError, got %T: %v", err, err)

	_, err = store.BySlug(ctx, "missing")
	assert.True(t, org.IsNotFoundError(err), "BySlug: want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_SaveIsUpsert(t *testing.T) {
	store, tc, _ := newPostgresStoreForTest(t)

	o, err := org.NewOrg("acme", "Acme", tc.UserID)
	require.NoError(t, err)
	ctx := scopedTo(t, tc, o.ID)
	require.NoError(t, store.Save(ctx, o))

	o.Name = "Acme Corp"
	require.NoError(t, store.Save(ctx, o))

	got, err := store.ByID(ctx, o.ID)
	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", got.Name)
}

func TestPostgres_MembershipRoundTrip(t *testing.T) {
	store, tc, _ := newPostgresStoreForTest(t)

	o, err := org.NewOrg("acme", "Acme", tc.UserID)
	require.NoError(t, err)
	ctx := scopedTo(t, tc, o.ID)
	require.NoError(t, store.Save(ctx, o))

	m, err := org.NewMembership(o.ID, tc.UserID, org.RoleOwner)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(ctx, m))

	got, err := store.MembershipOf(ctx, o.ID, tc.UserID)
	require.NoError(t, err)
	assert.Equal(t, org.RoleOwner, got.Role)

	members, err := store.ListMembers(ctx, o.ID)
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, tc.UserID, members[0].UserID)

	profiles, err := store.ListMemberProfiles(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Contains(t, profiles[0].Email, "@x.co")

	require.NoError(t, store.RemoveMember(ctx, o.ID, tc.UserID))
	_, err = store.MembershipOf(ctx, o.ID, tc.UserID)
	assert.True(t, org.IsMembershipMissingError(err) || org.IsNotFoundError(err), "want missing, got %T: %v", err, err)
}
