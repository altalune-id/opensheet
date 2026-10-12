//go:build integration

package schema_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestMigrateUp_Postgres_OneSystemIndexDemotesExistingDuplicates(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	prefix := "t" + suffix + "_"
	migDB := migrationRoleDB(t, h, "opensheet_onesys_"+suffix, "BYPASSRLS")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 16))

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	user := uuid.New()
	_, err := migDB.ExecContext(t.Context(),
		"INSERT INTO public."+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',true,$3,$3)",
		user, user.String()+"@x.co", t0)
	require.NoError(t, err)

	earliest, tiedLow, tiedHigh := uuid.MustParse("ffffffff-0000-4000-8000-000000000000"),
		uuid.MustParse("00000000-0000-4000-8000-000000000001"), uuid.MustParse("00000000-0000-4000-8000-000000000002")
	for _, o := range []struct {
		id uuid.UUID
		at time.Time
	}{{earliest, t0}, {tiedLow, t0.Add(time.Hour)}, {tiedHigh, t0.Add(time.Hour)}} {
		_, err := migDB.ExecContext(t.Context(),
			"INSERT INTO public."+prefix+"orgs (id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,'O',true,$3,$4,$4)",
			o.id, "o-"+o.id.String(), user, o.at)
		require.NoError(t, err)
	}
	firstProject, secondProject := uuid.New(), uuid.New()
	for i, p := range []uuid.UUID{firstProject, secondProject} {
		_, err := migDB.ExecContext(t.Context(),
			"INSERT INTO public."+prefix+"projects (id, org_id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,$3,'P',true,$4,$5,$5)",
			p, tiedLow, "p-"+p.String(), user, t0.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg), "duplicate system rows must be demoted, not fail the migration")

	var sysOrg uuid.UUID
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		"SELECT id FROM public."+prefix+"orgs WHERE system").Scan(&sysOrg))
	require.Equal(t, earliest, sysOrg, "the kept org must be the one SystemOrg resolves: earliest created_at, then id")

	var sysProject uuid.UUID
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		"SELECT id FROM public."+prefix+"projects WHERE system").Scan(&sysProject))
	require.Equal(t, firstProject, sysProject)
}
