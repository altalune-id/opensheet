package onboard_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/platform/config"
	pdb "altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/user"
	"altalune.id/template/schema"
)

// TestMain warms up modernc.org/sqlite's global mutex-init on the main goroutine so parallel Open calls under -race don't hit the driver-level data race in _sqlite3MutexInit.
func TestMain(m *testing.M) {
	db, err := sql.Open("sqlite", ":memory:")
	if err == nil {
		_ = db.Ping()
		_ = db.Close()
	}
	os.Exit(m.Run())
}

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

func seedUser(t *testing.T, sqlDB *sql.DB, dbcfg pdb.DBConfig) uuid.UUID {
	t.Helper()
	store := user.NewStore(dbcfg, pdb.Pool{W: sqlDB, R: sqlDB})
	u, err := user.New("root@example.com", "Root", user.SourceGenesis)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestSQLiteStore_Get_NotOnboarded(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := onboard.NewStore(dbcfg, pdb.Pool{W: db, R: db})
	_, err := store.Get(context.Background())
	if !onboard.IsNotOnboardedError(err) {
		t.Fatalf("want IsNotOnboardedError, got %T: %v", err, err)
	}
}

func TestSQLiteStore_SaveAndGet(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	userID := seedUser(t, db, dbcfg)
	store := onboard.NewStore(dbcfg, pdb.Pool{W: db, R: db})
	now := time.Now().UTC().Truncate(time.Second)
	b := &onboard.Bootstrap{OnboardedAt: now, OnboardedBy: userID, Method: onboard.MethodEnvGenesis}
	if err := store.Save(context.Background(), b); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.OnboardedBy != userID {
		t.Errorf("by mismatch")
	}
	if got.Method != onboard.MethodEnvGenesis {
		t.Errorf("method=%q", got.Method)
	}
	if !got.OnboardedAt.Equal(now) {
		t.Errorf("time mismatch: got %v want %v", got.OnboardedAt, now)
	}
}

func TestSQLiteStore_SaveIsSingleton(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	userID := seedUser(t, db, dbcfg)
	store := onboard.NewStore(dbcfg, pdb.Pool{W: db, R: db})
	b := &onboard.Bootstrap{OnboardedAt: time.Now().UTC(), OnboardedBy: userID, Method: onboard.MethodEnvGenesis}
	if err := store.Save(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	err := store.Save(context.Background(), b)
	if !onboard.IsAlreadyOnboardedError(err) {
		t.Fatalf("want IsAlreadyOnboardedError, got %T: %v", err, err)
	}
}
