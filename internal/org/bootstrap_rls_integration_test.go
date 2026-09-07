//go:build integration

package org_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/schema"
)

// Boot, web onboarding and `altempl init` all call BootstrapSingleton with no tenant in context,
// which is the one shape no other postgres test covers.
func TestPostgres_BootstrapSingleton_WithoutTenantContext_UnderRLS(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = false
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	prefix := cfg.DB.TablePrefix
	ownerID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', true, $3, $3)",
		ownerID, ownerID.String()+"@x.co", now,
	)
	require.NoError(t, err)

	store := org.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		tenant.NewPgConn(sqlDB),
	)
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("altempl.unexpected", msg, codes.Internal, &apperrorv1.ErrorDetail{Code: "altempl.unexpected"}).WithCause(cause)
	}
	svc := org.NewService(store, capabilities.Capabilities{}, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected)

	o, err := svc.BootstrapSingleton(t.Context(), "acme", "Acme", ownerID)
	require.NoError(t, err, "bootstrap must not require a tenant scope it cannot have yet")
	require.NotNil(t, o)
	require.True(t, o.System)

	var count int
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM "+prefix+"orgs WHERE slug = $1", "acme").Scan(&count))
	require.Equal(t, 1, count, "the org row must actually be committed")

	m, err := store.MembershipOf(tenant.Into(t.Context(), tenant.Context{OrgID: o.ID, UserID: ownerID}), o.ID, ownerID)
	require.NoError(t, err, "owner membership must be written under the same scope")
	require.True(t, m.System)
}
