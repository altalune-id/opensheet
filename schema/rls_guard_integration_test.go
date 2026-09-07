//go:build integration

package schema

import (
	"context"
	"errors"
	"slices"
	"testing"

	pcfg "altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/testutil/pgtest"
)

func TestRLSGuard_Integration(t *testing.T) {
	conn := pgtest.New(t).OpenDB(t)

	ctx := context.Background()
	const table = "altempl_todos"

	var bypassRLS bool
	if err := conn.QueryRowContext(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&bypassRLS); err != nil {
		t.Fatalf("probe current_user: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE altempl_todos (
			id     UUID PRIMARY KEY,
			org_id UUID NOT NULL,
			title  TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE altempl_todos ENABLE ROW LEVEL SECURITY`); err != nil {
		t.Fatalf("enable rls: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE altempl_todos FORCE ROW LEVEL SECURITY`); err != nil {
		t.Fatalf("force rls: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE POLICY altempl_todos_tenant ON altempl_todos
		USING (org_id = current_setting('app.current_org_id')::uuid)
	`); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	cfg := &pcfg.Config{
		DB: db.DBConfig{
			Driver:         db.DriverPostgres,
			AllowBypassRLS: bypassRLS,
		},
		Tenant: pcfg.TenantConfig{
			TenantScopedTables: []string{table},
		},
	}

	if bypassRLS {
		if err := AuditPolicies(ctx, conn, []string{table}); err != nil {
			t.Fatalf("AuditPolicies passing case: %v", err)
		}
	} else {
		if err := RLSGuard(ctx, conn, cfg); err != nil {
			t.Fatalf("RLSGuard passing case: %v", err)
		}
	}

	if _, err := conn.ExecContext(ctx, `DROP POLICY altempl_todos_tenant ON altempl_todos`); err != nil {
		t.Fatalf("drop policy: %v", err)
	}

	var auditErr error
	if bypassRLS {
		auditErr = AuditPolicies(ctx, conn, []string{table})
	} else {
		auditErr = RLSGuard(ctx, conn, cfg)
	}
	if auditErr == nil {
		t.Fatal("expected error after policy dropped, got nil")
	}
	if !IsRLSAuditError(auditErr) {
		t.Fatalf("err = %v; want *RLSAuditError", auditErr)
	}
	var audit *RLSAuditError
	if !errors.As(auditErr, &audit) {
		t.Fatalf("errors.As(*RLSAuditError): failed for %v", auditErr)
	}
	if !slices.Contains(audit.MissingPolicy, table) {
		t.Errorf("MissingPolicy = %v; want to include %q", audit.MissingPolicy, table)
	}
}

func TestAuditPolicies_AcceptsMigratedHelperScopedPolicies(t *testing.T) {
	h := pgtest.New(t)
	conn := h.OpenDB(t)

	cfg := pcfg.Defaults()
	cfg.DB.Driver = db.DriverPostgres
	cfg.DB.Schema = h.Schema
	if err := MigrateUp(t.Context(), conn, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	if err := AuditPolicies(t.Context(), conn, TenantTableNames(cfg.DB.TablePrefix)); err != nil {
		t.Fatalf("AuditPolicies rejected the policies migration 002 creates: %v", err)
	}
}
