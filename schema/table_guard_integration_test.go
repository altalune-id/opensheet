//go:build integration

package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestAssertRequiredTables_PostgresMigratedDatabaseIsComplete(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	prefix := "t" + suffix + "_"

	migDB := migrationRoleDB(t, h, "opensheet_tblguard_"+suffix, "BYPASSRLS")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	require.NotEmpty(t, schema.RequiredTableSuffixes, "the guard would pass vacuously")
	require.NoError(t, schema.AssertRequiredTables(t.Context(), migDB, &cfg.DB))

	_, err := migDB.ExecContext(t.Context(), `DROP TABLE `+prefix+`sessions`)
	require.NoError(t, err)

	err = schema.AssertRequiredTables(t.Context(), migDB, &cfg.DB)
	require.True(t, schema.IsStaleSchemaError(err), "want *StaleSchemaError, got %v", err)
	require.Contains(t, err.Error(), prefix+"sessions")
}
