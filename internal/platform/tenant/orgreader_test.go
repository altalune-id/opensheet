package tenant

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
)

func TestNewOrgReader_PostgresReadsTheDefinerWrapper(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverPostgres, "public", "altempl_").(*pgOrgReader)
	require.True(t, ok, "postgres must select the wrapper-backed reader")
	require.Contains(t, r.query, "public.altempl_list_org_ids()")
	require.NotContains(t, r.query, "altempl_orgs",
		"SECURITY: reading altempl_orgs directly returns zero rows under FORCE row level security")
	require.Empty(t, r.args)
}

func TestNewOrgReader_PostgresDefaultsBlankSchemaToPublic(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverPostgres, "", "altempl_").(*pgOrgReader)
	require.True(t, ok)
	require.Contains(t, r.query, "public.altempl_list_org_ids()")
}

func TestNewOrgReader_SQLiteReadsTheTable(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, db.DriverSQLite, "", "altempl_").(*sqliteOrgReader)
	require.True(t, ok, "sqlite carries no definer wrappers")
	require.Contains(t, r.query, "altempl_orgs")
	require.Contains(t, r.query, "ORDER BY orgs.created_at ASC")
	require.Empty(t, r.args)
}
