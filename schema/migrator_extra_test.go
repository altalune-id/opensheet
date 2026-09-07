package schema

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
)

func memMapFS() fstest.MapFS {
	return fstest.MapFS{
		"001.sql": &fstest.MapFile{Data: []byte(`SELECT '{{.TablePrefix}}';`)},
	}
}

func TestGooseDialect_Unknown(t *testing.T) {
	t.Parallel()
	_, _, err := gooseDialect(db.Driver("bogus"))
	require.Error(t, err)
}

func TestGooseDialect_SQLite(t *testing.T) {
	t.Parallel()
	d, path, err := gooseDialect(db.DriverSQLite)
	require.NoError(t, err)
	assert.Equal(t, "migrations/sqlite", path)
	assert.NotEmpty(t, string(d))
}

func TestGooseDialect_Postgres(t *testing.T) {
	t.Parallel()
	d, path, err := gooseDialect(db.DriverPostgres)
	require.NoError(t, err)
	assert.Equal(t, "migrations/postgres", path)
	assert.NotEmpty(t, string(d))
}

func TestMigrationsBookkeepingTable_Sqlite(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	assert.Equal(t, "altempl_goose_db_version", migrationsBookkeepingTable(cfg))
}

func TestMigrationsBookkeepingTable_Postgres(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverPostgres
	cfg.DB.TablePrefix = "altempl_"
	assert.Equal(t, "altempl_goose_db_version", migrationsBookkeepingTable(cfg))
}

func TestMigrateStatus_ReportsPending(t *testing.T) {
	t.Parallel()
	sqldb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })

	cfg := config.Defaults()
	rows, err := MigrateStatus(context.Background(), sqldb, cfg)
	require.NoError(t, err)
	assert.NotEmpty(t, rows)
	for _, r := range rows {
		assert.False(t, r.Applied)
		assert.NotEmpty(t, r.Source)
	}
}

func TestMigrateStatus_ReportsApplied(t *testing.T) {
	t.Parallel()
	sqldb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })

	cfg := config.Defaults()
	require.NoError(t, MigrateUp(context.Background(), sqldb, cfg))
	rows, err := MigrateStatus(context.Background(), sqldb, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	applied := 0
	for _, r := range rows {
		if r.Applied {
			applied++
		}
	}
	assert.Positive(t, applied)
}

func TestMigrateDownTo_RollsBackToZero(t *testing.T) {
	t.Parallel()
	sqldb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })

	cfg := config.Defaults()
	require.NoError(t, MigrateUp(context.Background(), sqldb, cfg))
	require.NoError(t, MigrateDownTo(context.Background(), sqldb, cfg, 0))

	rows, err := MigrateStatus(context.Background(), sqldb, cfg)
	require.NoError(t, err)
	for _, r := range rows {
		assert.False(t, r.Applied, "version %d should be rolled back", r.Version)
	}
}

func TestTemplatedFS_MemoryFileStatFields(t *testing.T) {
	t.Parallel()
	base := memMapFS()
	tfs := newTemplatedFS(base, templateVars{TablePrefix: "altempl_"})
	f, err := tfs.Open("001.sql")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	info, err := f.Stat()
	require.NoError(t, err)
	assert.Equal(t, "001.sql", info.Name())
	assert.False(t, info.IsDir())
	assert.NotZero(t, info.Mode())
	assert.NotZero(t, info.Size())
	_ = info.ModTime()
	assert.Nil(t, info.Sys())
}

func TestMigrateUp_UnknownDriverErrors(t *testing.T) {
	t.Parallel()
	sqldb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = db.Driver("bogus")
	require.Error(t, MigrateUp(context.Background(), sqldb, cfg))
	_, err = MigrateStatus(context.Background(), sqldb, cfg)
	require.Error(t, err)
	require.Error(t, MigrateDownTo(context.Background(), sqldb, cfg, 0))
}
