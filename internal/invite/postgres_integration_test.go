//go:build integration

package invite_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/schema"
)

func newInviteStoreForTest(t *testing.T) (invite.Store, uuid.UUID, uuid.UUID) { //nolint:nonamedreturns // triple
	t.Helper()
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

	pc := tenant.NewPgConn(sqlDB)
	store := invite.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, pc)
	return store, orgID, userID
}

func seedInviteUserAndOrg(t *testing.T, sqlDB *sql.DB, prefix string) (uuid.UUID, uuid.UUID) { //nolint:nonamedreturns // pair
	t.Helper()
	userID := uuid.New()
	orgID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@x.co", now,
	)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'acme', 'Acme', $2, $3, $3)",
		orgID, userID, now,
	)
	require.NoError(t, err)
	return userID, orgID
}

func newInvite(t *testing.T, orgID uuid.UUID, email, token string) *invite.Invite {
	t.Helper()
	i, err := invite.New(invite.NewParams{
		OrgID: orgID, Email: email, Role: invite.RoleMember,
		TTL: time.Hour, Token: token, Now: time.Now().UTC(),
	})
	require.NoError(t, err)
	return i
}

func TestPostgres_Invite_SaveAndLookup(t *testing.T) {
	store, orgID, userID := newInviteStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	i := newInvite(t, orgID, "guest@example.com", "s3cret")
	require.NoError(t, store.Save(ctx, i))

	got, err := store.ByID(ctx, i.ID)
	require.NoError(t, err)
	assert.Equal(t, "guest@example.com", got.Email)

	byHash, err := store.ByTokenHash(ctx, invite.HashToken("s3cret"))
	require.NoError(t, err)
	assert.Equal(t, i.ID, byHash.ID)
}

func TestPostgres_Invite_NotFound(t *testing.T) {
	store, orgID, userID := newInviteStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	_, err := store.ByID(ctx, uuid.New())
	assert.True(t, invite.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)

	_, err = store.ByTokenHash(ctx, invite.HashToken("no-such"))
	assert.True(t, invite.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Invite_ListPending(t *testing.T) {
	store, orgID, userID := newInviteStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	i1 := newInvite(t, orgID, "a@x.co", "t1")
	require.NoError(t, store.Save(ctx, i1))
	i2 := newInvite(t, orgID, "b@x.co", "t2")
	require.NoError(t, store.Save(ctx, i2))

	got, err := store.ListPending(ctx, orgID)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestPostgres_Invite_Delete(t *testing.T) {
	store, orgID, userID := newInviteStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	i := newInvite(t, orgID, "revoke@example.com", "t")
	require.NoError(t, store.Save(ctx, i))
	require.NoError(t, store.Delete(ctx, i.ID))

	_, err := store.ByID(ctx, i.ID)
	assert.True(t, invite.IsNotFoundError(err), "want NotFoundError after delete, got %T: %v", err, err)
}
