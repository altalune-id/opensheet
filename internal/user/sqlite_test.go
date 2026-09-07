package user_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"altalune.id/template/internal/platform/config"
	pdb "altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/user"
	"altalune.id/template/schema"
)

func openMemSQLite(t *testing.T) (*sql.DB, pdb.DBConfig) {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Defaults()
	if err := schema.MigrateUp(context.Background(), db, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, pdb.DBConfig{
		Driver:      pdb.DriverSQLite,
		DSN:         ":memory:",
		TablePrefix: cfg.DB.TablePrefix,
	}
}

func TestSQLiteStore_SaveAndLookup(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})

	u, err := user.New("Alice@Example.com", "Alice", user.SourceGenesis)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), u); err != nil {
		t.Fatalf("Save: %v", err)
	}

	byID, err := store.ByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if byID.Email != "alice@example.com" {
		t.Errorf("email=%q", byID.Email)
	}
	if byID.Source != user.SourceGenesis {
		t.Errorf("source=%q", byID.Source)
	}

	byEmail, err := store.ByEmail(context.Background(), "Alice@Example.com")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if byEmail.ID != u.ID {
		t.Errorf("ID mismatch")
	}
}

func TestSQLiteStore_NotFound(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})

	_, err := store.ByEmail(context.Background(), "missing@example.com")
	if err == nil {
		t.Fatal("expected NotFoundError")
	}
	if !user.IsNotFoundError(err) {
		t.Errorf("want IsNotFoundError, got %T: %v", err, err)
	}
}

func TestSQLiteStore_UpsertPreservesID(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})

	u, _ := user.New("a@b.co", "old", user.SourceGenesis)
	if err := store.Save(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := u.Rename("new"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), u); err != nil {
		t.Fatal(err)
	}

	got, err := store.ByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new" {
		t.Errorf("name not upserted: %q", got.Name)
	}
}

func TestSQLiteStore_UniqueEmailViolation(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})

	a, _ := user.New("dup@example.com", "A", user.SourceGenesis)
	b, _ := user.New("dup@example.com", "B", user.SourceOIDC)
	if err := store.Save(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	err := store.Save(context.Background(), b)
	if err == nil {
		t.Fatal("expected AlreadyExistsError on duplicate email")
	}
	if !user.IsAlreadyExistsError(err) {
		t.Errorf("want IsAlreadyExistsError, got %T: %v", err, err)
	}
}
