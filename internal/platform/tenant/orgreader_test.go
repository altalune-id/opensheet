package tenant

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
)

func TestNewOrgReader_PostgresReadsTheDefinerWrapper(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverPostgres, "public", "opensheet_").(*pgOrgReader)
	require.True(t, ok, "postgres must select the wrapper-backed reader")
	require.Contains(t, r.query, "public.opensheet_list_org_ids()")
	require.NotContains(t, r.query, "opensheet_orgs",
		"SECURITY: reading opensheet_orgs directly returns zero rows under FORCE row level security")
	require.Empty(t, r.args)
}

func TestNewOrgReader_PostgresDefaultsBlankSchemaToPublic(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverPostgres, "", "opensheet_").(*pgOrgReader)
	require.True(t, ok)
	require.Contains(t, r.query, "public.opensheet_list_org_ids()")
}

func TestNewOrgReader_SQLiteReadsTheTable(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverSQLite, "", "opensheet_").(*sqliteOrgReader)
	require.True(t, ok, "sqlite carries no definer wrappers")
	require.Contains(t, r.query, "opensheet_orgs")
	require.Contains(t, r.query, "ORDER BY orgs.created_at ASC")
	require.Empty(t, r.args)
}

func TestNewOrgReader_PostgresOrdersOnTheOuterStatement(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverPostgres, "public", "opensheet_").(*pgOrgReader)
	require.True(t, ok)
	require.Contains(t, r.query, "public.opensheet_list_org_ids() o",
		"the set-returning function must be aliased, or the outer ORDER BY cannot reference it")
	require.Contains(t, r.query, "ORDER BY o.created_at ASC, o.id ASC",
		"postgres does not guarantee an ORDER BY inside an inlinable SQL SRF survives into an outer unordered SELECT")
}
