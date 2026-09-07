package project

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/schema"
)

func newSQLiteDBForTest(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("foreign_keys pragma: %v", err)
	}
	cfg := config.Defaults()
	if err := schema.MigrateUp(context.Background(), db, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return db, cfg.DB.TablePrefix
}

var _ Store = (*sqliteStore)(nil)

func seedUserAndOrg(t *testing.T, db *sql.DB) (userID, orgID uuid.UUID) {
	t.Helper()
	userID = uuid.New()
	orgID = uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(
		`INSERT INTO altempl_users (id, email, name, avatar_url, is_admin, created_at, updated_at)
		 VALUES (?, ?, '', '', 0, ?, ?)`,
		userID.String(), userID.String()+"@example.com", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO altempl_orgs (id, slug, name, created_by, created_at, updated_at)
		 VALUES (?, ?, 'Org', ?, ?, ?)`,
		orgID.String(), orgID.String()[:8], userID.String(), now, now); err != nil {
		t.Fatal(err)
	}
	return
}

func tenantCtx(orgID, userID uuid.UUID) context.Context {
	return tenant.Into(context.Background(), tenant.Context{OrgID: orgID, UserID: userID})
}

func newStoreWithSeed(t *testing.T) (*sqliteStore, context.Context, uuid.UUID) {
	t.Helper()
	db, prefix := newSQLiteDBForTest(t)
	userID, orgID := seedUserAndOrg(t, db)
	return newSQLiteStore(db, prefix), tenantCtx(orgID, userID), orgID
}

func TestSQLite_SaveAndBySlug(t *testing.T) {
	store, ctx, orgID := newStoreWithSeed(t)
	p, err := New(orgID, "web", "Web")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.BySlug(ctx, orgID, "web")
	if err != nil {
		t.Fatalf("BySlug: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("id mismatch: got=%s want=%s", got.ID, p.ID)
	}
	if got.Name != "Web" {
		t.Errorf("name=%q want Web", got.Name)
	}
}

func TestSQLite_ByID_NotFound(t *testing.T) {
	store, ctx, _ := newStoreWithSeed(t)
	_, err := store.ByID(ctx, uuid.New())
	if !IsNotFoundError(err) {
		t.Errorf("want *NotFoundError, got %T (%v)", err, err)
	}
}

func TestSQLite_BySlug_NotFound(t *testing.T) {
	store, ctx, orgID := newStoreWithSeed(t)
	_, err := store.BySlug(ctx, orgID, "missing")
	if !IsNotFoundError(err) {
		t.Errorf("want *NotFoundError, got %T (%v)", err, err)
	}
}

func TestSQLite_List(t *testing.T) {
	store, ctx, orgID := newStoreWithSeed(t)
	p1, _ := New(orgID, "web", "Web")
	p2, _ := New(orgID, "api", "API")
	if err := store.Save(ctx, p1); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, p2); err != nil {
		t.Fatal(err)
	}
	got, err := store.List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
}

func TestSQLite_Save_UpsertRename(t *testing.T) {
	store, ctx, orgID := newStoreWithSeed(t)
	p, _ := New(orgID, "web", "Web")
	if err := store.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Rename("Web Site"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := store.ByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Web Site" {
		t.Errorf("name=%q want %q", got.Name, "Web Site")
	}
}

func TestSQLite_Save_SlugUnique(t *testing.T) {
	store, ctx, orgID := newStoreWithSeed(t)
	p1, _ := New(orgID, "web", "Web")
	if err := store.Save(ctx, p1); err != nil {
		t.Fatal(err)
	}
	p2, _ := New(orgID, "web", "Web 2")
	err := store.Save(ctx, p2)
	if !IsAlreadyExistsError(err) {
		t.Errorf("want *AlreadyExistsError, got %T (%v)", err, err)
	}
}

func TestSQLite_SystemFlag_Roundtrip(t *testing.T) {
	store, ctx, orgID := newStoreWithSeed(t)
	p, _ := New(orgID, "web", "Web")
	p.System = true
	if err := store.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := store.ByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.System {
		t.Errorf("System = false, want true")
	}
}

func TestSQLite_MissingTenant(t *testing.T) {
	db, prefix := newSQLiteDBForTest(t)
	store := newSQLiteStore(db, prefix)
	if err := store.Save(context.Background(), &Project{ID: uuid.New()}); !tenant.IsMissingError(err) {
		t.Errorf("Save: want *tenant.MissingError, got %T", err)
	}
	if _, err := store.ByID(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
		t.Errorf("ByID: want *tenant.MissingError, got %T", err)
	}
	if _, err := store.BySlug(context.Background(), uuid.New(), "x"); !tenant.IsMissingError(err) {
		t.Errorf("BySlug: want *tenant.MissingError, got %T", err)
	}
	if _, err := store.List(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
		t.Errorf("List: want *tenant.MissingError, got %T", err)
	}
}
