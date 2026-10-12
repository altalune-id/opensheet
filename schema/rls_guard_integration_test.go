//go:build integration

package schema

import (
	"context"
	"errors"
	"slices"
	"testing"

	pcfg "altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/testutil/pgtest"
)

func TestRLSGuard_Integration(t *testing.T) {
	conn := pgtest.New(t).OpenDB(t)

	ctx := context.Background()
	const table = "opensheet_todos"

	var bypassRLS bool
	if err := conn.QueryRowContext(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&bypassRLS); err != nil {
		t.Fatalf("probe current_user: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE opensheet_todos (
			id     UUID PRIMARY KEY,
			org_id UUID NOT NULL,
			title  TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE opensheet_todos ENABLE ROW LEVEL SECURITY`); err != nil {
		t.Fatalf("enable rls: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE opensheet_todos FORCE ROW LEVEL SECURITY`); err != nil {
		t.Fatalf("force rls: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE POLICY opensheet_todos_tenant ON opensheet_todos
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

	if _, err := conn.ExecContext(ctx, `DROP POLICY opensheet_todos_tenant ON opensheet_todos`); err != nil {
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

func TestAuditPolicies_PolicyShapes(t *testing.T) {
	const scoped = `(org_id = current_setting('app.current_org_id')::uuid)`

	tests := []struct {
		name    string
		policy  string
		wantErr bool
		bucket  func(*RLSAuditError) []string
	}{
		{
			name:   "for all using only passes",
			policy: `CREATE POLICY opensheet_todos_tenant ON opensheet_todos USING ` + scoped,
		},
		{
			name:   "for all with an explicitly scoped with check passes",
			policy: `CREATE POLICY opensheet_todos_tenant ON opensheet_todos USING ` + scoped + ` WITH CHECK ` + scoped,
		},
		{
			name:    "explicit with check true fails",
			policy:  `CREATE POLICY opensheet_todos_tenant ON opensheet_todos USING ` + scoped + ` WITH CHECK (true)`,
			wantErr: true,
			bucket:  func(e *RLSAuditError) []string { return e.UnscopedPolicy },
		},
		{
			name:    "for select only fails",
			policy:  `CREATE POLICY opensheet_todos_tenant ON opensheet_todos FOR SELECT USING ` + scoped,
			wantErr: true,
			bucket:  func(e *RLSAuditError) []string { return e.MissingWritePolicy },
		},
		{
			name: "unscoped sibling policy fails",
			policy: `CREATE POLICY opensheet_todos_tenant ON opensheet_todos USING ` + scoped + `;
				CREATE POLICY opensheet_todos_open ON opensheet_todos FOR SELECT USING (true)`,
			wantErr: true,
			bucket:  func(e *RLSAuditError) []string { return e.UnscopedPolicy },
		},
	}

	// NOTE: one container for every case — pgtest.New starts a Postgres per call, and a container
	// per subtest pushed this package past its 10 minute timeout.
	conn := pgtest.New(t).OpenDB(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS opensheet_todos CASCADE`); err != nil {
				t.Fatalf("drop table: %v", err)
			}
			if _, err := conn.ExecContext(ctx, `
				CREATE TABLE opensheet_todos (
					id     UUID PRIMARY KEY,
					org_id UUID NOT NULL,
					title  TEXT NOT NULL
				);
				ALTER TABLE opensheet_todos ENABLE ROW LEVEL SECURITY;
				ALTER TABLE opensheet_todos FORCE ROW LEVEL SECURITY;
			`); err != nil {
				t.Fatalf("create table: %v", err)
			}
			if _, err := conn.ExecContext(ctx, tt.policy); err != nil {
				t.Fatalf("create policy: %v", err)
			}

			err := AuditPolicies(ctx, conn, []string{"opensheet_todos"})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("AuditPolicies = %v; want nil", err)
				}
				return
			}
			var audit *RLSAuditError
			if !errors.As(err, &audit) {
				t.Fatalf("AuditPolicies = %v; want *RLSAuditError", err)
			}
			if got := tt.bucket(audit); !slices.Contains(got, "opensheet_todos") {
				t.Errorf("bucket = %v; want to include %q (full audit: %+v)", got, "opensheet_todos", audit)
			}
		})
	}
}
