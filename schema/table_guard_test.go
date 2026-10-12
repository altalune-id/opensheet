package schema

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pcfg "altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
)

func migratedSQLite(t *testing.T) (*sql.DB, *pcfg.Config) {
	t.Helper()
	cfg := pcfg.Defaults()
	cfg.DB.Driver = db.DriverSQLite
	cfg.DB.DSN = filepath.Join(t.TempDir(), "opensheet.db")

	conn, err := db.Open(t.Context(), cfg.DB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := MigrateUp(t.Context(), conn, cfg); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	return conn, cfg
}

func TestAssertRequiredTables_SQLiteMigratedDatabaseIsComplete(t *testing.T) {
	conn, cfg := migratedSQLite(t)

	if len(RequiredTableSuffixes) == 0 {
		t.Fatal("RequiredTableSuffixes is empty; the guard would pass vacuously")
	}
	if err := AssertRequiredTables(t.Context(), conn, &cfg.DB); err != nil {
		t.Fatalf("AssertRequiredTables on a freshly migrated database: %v", err)
	}
}

func TestAssertRequiredTables_SQLiteMissingTableIsStale(t *testing.T) {
	conn, cfg := migratedSQLite(t)
	missing := cfg.DB.TablePrefix + "sessions"

	if _, err := conn.ExecContext(t.Context(), `DROP TABLE `+missing); err != nil {
		t.Fatalf("drop %s: %v", missing, err)
	}

	err := AssertRequiredTables(t.Context(), conn, &cfg.DB)
	if err == nil {
		t.Fatal("AssertRequiredTables returned nil for a database missing the sessions table")
	}
	if !IsStaleSchemaError(err) {
		t.Fatalf("IsStaleSchemaError = false for %T: %v", err, err)
	}
	stale, ok := errors.AsType[*StaleSchemaError](err)
	if !ok {
		t.Fatalf("error is not a *StaleSchemaError: %T", err)
	}
	if !slices.Contains(stale.Missing, missing) {
		t.Errorf("Missing = %v, want it to name %q", stale.Missing, missing)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error message %q does not name the missing table %q", err.Error(), missing)
	}
}

func TestAssertRequiredTables_HonoursTablePrefix(t *testing.T) {
	conn, cfg := migratedSQLite(t)
	cfg.DB.TablePrefix = "other_"

	err := AssertRequiredTables(t.Context(), conn, &cfg.DB)
	if !IsStaleSchemaError(err) {
		t.Fatalf("want *StaleSchemaError for an unmigrated prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "other_sessions") {
		t.Errorf("error message %q does not use the configured prefix", err.Error())
	}
}

func TestIsStaleSchemaError_FalseForForeignError(t *testing.T) {
	if IsStaleSchemaError(errors.New("boom")) {
		t.Error("IsStaleSchemaError = true for a foreign error")
	}
	if IsStaleSchemaError(nil) {
		t.Error("IsStaleSchemaError = true for nil")
	}
}

func TestAssertRequiredTables_NilConfig(t *testing.T) {
	if err := AssertRequiredTables(context.Background(), nil, nil); err != nil {
		t.Fatalf("nil config must be a no-op, got %v", err)
	}
}

// TestExistingTables_EmptyWant pins the SQLite placeholder builder against a negative strings.Repeat count.
func TestExistingTables_EmptyWant(t *testing.T) {
	conn, _ := migratedSQLite(t)

	present, err := existingTables(t.Context(), conn, db.DriverSQLite, nil)
	if err != nil {
		t.Fatalf("existingTables with no wanted tables: %v", err)
	}
	if len(present) != 0 {
		t.Errorf("existingTables with no wanted tables = %v, want empty", present)
	}
}
