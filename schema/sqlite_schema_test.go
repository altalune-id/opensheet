package schema

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
)

// TestSQLiteSchema_MatchesGolden pins the whole SQLite shape: sqlite_master carries the
// full CREATE text, so a dropped column or constraint fails here rather than at runtime.
func TestSQLiteSchema_MatchesGolden(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, MigrateUp(context.Background(), db, config.Defaults()))

	rows, err := db.QueryContext(context.Background(),
		`SELECT type, name, coalesce(sql, '') FROM sqlite_master
		  WHERE name NOT LIKE 'sqlite_%' AND name NOT LIKE '%goose%'
		  ORDER BY type, name`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var b strings.Builder
	for rows.Next() {
		var typ, name, ddl string
		require.NoError(t, rows.Scan(&typ, &name, &ddl))
		fmt.Fprintf(&b, "%s %s\n%s\n\n", typ, name, ddl)
	}
	require.NoError(t, rows.Err())

	golden := filepath.Join("testdata", "sqlite_schema.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(golden, []byte(b.String()), 0o600))
		return
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create the golden file")
	require.Equal(t, string(want), b.String(),
		"SQLite schema changed; re-read the diff before running UPDATE_GOLDEN=1")
}
