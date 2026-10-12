//go:build integration

package todo_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/schema"
)

func newTodoStoreForTest(t *testing.T) (todo.Store, uuid.UUID, uuid.UUID, uuid.UUID) { //nolint:nonamedreturns // multi-return
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
	userID, orgID, projID := seedProjectTree(t, sqlDB, prefix)

	pc := tenant.NewPgConn(sqlDB)
	store := todo.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, pc)
	return store, orgID, projID, userID
}

func seedProjectTree(t *testing.T, sqlDB *sql.DB, prefix string) (uuid.UUID, uuid.UUID, uuid.UUID) { //nolint:nonamedreturns // triple
	t.Helper()
	userID := uuid.New()
	orgID := uuid.New()
	projID := uuid.New()
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
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'web', 'Web', $3, $4, $4)",
		projID, orgID, userID, now,
	)
	require.NoError(t, err)
	return userID, orgID, projID
}

func TestPostgres_Todo_SaveAndLookup(t *testing.T) {
	store, orgID, projID, userID := newTodoStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID})

	todos := []*todo.Todo{}
	for _, title := range []string{"first", "second"} {
		td, err := todo.New(orgID, projID, title)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, td))
		todos = append(todos, td)
	}

	got, err := store.ByID(ctx, todos[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "first", got.Title)
}

func TestPostgres_Todo_NotFound(t *testing.T) {
	store, orgID, projID, userID := newTodoStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID})

	_, err := store.ByID(ctx, uuid.New())
	assert.True(t, todo.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Todo_List(t *testing.T) {
	store, orgID, projID, userID := newTodoStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID})

	for _, title := range []string{"a", "b", "c"} {
		td, err := todo.New(orgID, projID, title)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, td))
	}

	got, err := store.List(ctx, orgID, projID, todo.ListOpts{})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestPostgres_Todo_Delete(t *testing.T) {
	store, orgID, projID, userID := newTodoStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID})

	td, err := todo.New(orgID, projID, "gone")
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, td))
	require.NoError(t, store.Delete(ctx, td.ID))

	_, err = store.ByID(ctx, td.ID)
	assert.True(t, todo.IsNotFoundError(err), "want NotFoundError after delete, got %T: %v", err, err)
}

func TestPostgres_Todo_ClearDone(t *testing.T) {
	store, orgID, projID, userID := newTodoStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID})

	done, err := todo.New(orgID, projID, "done")
	require.NoError(t, err)
	done.Done = true
	require.NoError(t, store.Save(ctx, done))

	open, err := todo.New(orgID, projID, "open")
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, open))

	n, err := store.ClearDone(ctx, orgID, projID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	remaining, err := store.List(ctx, orgID, projID, todo.ListOpts{})
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "open", remaining[0].Title)
}

func TestPostgresStore_MarkDoneOlderThan_BatchesAndRespectsTenant(t *testing.T) {
	store, orgID, projID, userID := newTodoStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID})

	old := time.Now().UTC().Add(-20 * 24 * time.Hour)
	for i := range 5 {
		td, err := todo.New(orgID, projID, fmt.Sprintf("stale-%d", i))
		require.NoError(t, err)
		td.CreatedAt = old
		td.UpdatedAt = old
		require.NoError(t, store.Save(ctx, td))
	}
	fresh, err := todo.New(orgID, projID, "fresh")
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, fresh))

	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	n, err := store.MarkDoneOlderThan(ctx, orgID, cutoff, 2)
	require.NoError(t, err)
	assert.Equal(t, 5, n, "batching must loop until exhausted")

	no := false
	open, err := store.List(ctx, orgID, projID, todo.ListOpts{Done: &no})
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, "fresh", open[0].Title)

	n, err = store.MarkDoneOlderThan(ctx, uuid.New(), cutoff, 100)
	require.NoError(t, err)
	assert.Zero(t, n, "another org's scope must sweep nothing")
}

type todoServiceFixture struct {
	svc      *todo.Service
	keyStore apikey.Store
	orgID    uuid.UUID
	projID   uuid.UUID
	userID   uuid.UUID
}

func newTodoServiceFixture(t *testing.T) todoServiceFixture {
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
	userID, orgID, projID := seedProjectTree(t, sqlDB, prefix)

	pc := tenant.NewPgConn(sqlDB)
	dbCfg := db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}
	pool := db.Pool{W: sqlDB, R: sqlDB}
	todoStore := todo.NewStore(dbCfg, pool, pc)
	keyStore := apikey.NewStore(dbCfg, pool, pc)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	return todoServiceFixture{
		svc:      todo.NewService(todoStore, log, unexpected, &fakes.Queue{}),
		keyStore: keyStore,
		orgID:    orgID,
		projID:   projID,
		userID:   userID,
	}
}

// TestPostgres_Service_Create_KeyPrincipalPersistsNullUserID proves a key principal's row names the key and leaves user_id NULL.
func TestPostgres_Service_Create_KeyPrincipalPersistsNullUserID(t *testing.T) {
	f := newTodoServiceFixture(t)
	k, _, err := apikey.Scheme{}.Mint(f.orgID, f.projID, "ci key", []string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
	require.NoError(t, err)
	seedCtx := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.projID})
	require.NoError(t, f.keyStore.Save(seedCtx, k))

	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.projID})
	ctx = session.PrincipalInto(ctx, session.Principal{KeyID: k.ID, Source: session.SourceAPIKey})

	created, err := f.svc.Create(ctx, "minted by a key")
	require.NoError(t, err)
	assert.Equal(t, k.ID, created.Author.KeyID)
	assert.Equal(t, uuid.Nil, created.Author.UserID)

	got, err := f.svc.ByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, k.ID, got.Author.KeyID, "the row read back from Postgres must still name the key")
	assert.Equal(t, uuid.Nil, got.Author.UserID, "a key-authored row must persist a NULL user_id")
}

// TestPostgres_Service_Create_UserPrincipalPersistsNullKeyID is the control case for a human principal's row.
func TestPostgres_Service_Create_UserPrincipalPersistsNullKeyID(t *testing.T) {
	f := newTodoServiceFixture(t)

	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.projID, UserID: f.userID})
	ctx = session.PrincipalInto(ctx, session.Principal{UserID: f.userID, Source: session.SourceOIDC})

	created, err := f.svc.Create(ctx, "written by a person")
	require.NoError(t, err)
	assert.Equal(t, f.userID, created.Author.UserID)
	assert.Equal(t, uuid.Nil, created.Author.KeyID)

	got, err := f.svc.ByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, f.userID, got.Author.UserID, "the row read back from Postgres must still name the user")
	assert.Equal(t, uuid.Nil, got.Author.KeyID, "a user-authored row must persist a NULL created_by_key_id")
}
