//go:generate go tool gen-tenant-tables

package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	pcfg "altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
)

// ErrRLSBypass is returned when the app connection's role has BYPASSRLS.
var ErrRLSBypass = errors.New("rls guard: connecting role has BYPASSRLS")

const currentOrgIDGUC = "app.current_org_id"

// RLSAuditError enumerates tenant-scoped tables whose RLS posture is wrong.
type RLSAuditError struct {
	MissingRLS    []string
	MissingForce  []string
	MissingPolicy []string
}

func (e *RLSAuditError) Error() string {
	return fmt.Sprintf(
		"rls audit: %d missing RLS, %d missing FORCE, %d missing tenant policy",
		len(e.MissingRLS), len(e.MissingForce), len(e.MissingPolicy),
	)
}

// IsRLSAuditError reports whether err's tree contains a *RLSAuditError.
func IsRLSAuditError(err error) bool {
	_, ok := errors.AsType[*RLSAuditError](err)
	return ok
}

// CheckRLSGuard is the legacy boot-time BYPASSRLS check.
func CheckRLSGuard(ctx context.Context, conn *sql.DB, allowBypass bool) error {
	return checkBypassRLS(ctx, conn, allowBypass)
}

// RLSGuard is the boot-time posture check: BYPASSRLS role rejection plus a pg_policies audit against tenant-scoped tables.
func RLSGuard(ctx context.Context, conn *sql.DB, cfg *pcfg.Config) error {
	if cfg == nil {
		return errors.New("rls guard: nil config")
	}
	if cfg.DB.Driver != db.DriverPostgres {
		return nil
	}
	if cfg.DB.AllowBypassRLS {
		slog.Default().Warn("rls guard: db.allowBypassRLS is true — tenant isolation checks skipped (dev only)")
		return nil
	}
	if err := checkBypassRLS(ctx, conn, false); err != nil {
		return err
	}
	tables := cfg.Tenant.TenantScopedTables
	if len(tables) == 0 {
		tables = TenantTableNames(cfg.DB.TablePrefix)
	}
	return AuditPolicies(ctx, conn, tables)
}

func checkBypassRLS(ctx context.Context, conn *sql.DB, allowBypass bool) error {
	var bypass bool
	if err := conn.QueryRowContext(ctx,
		"SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user",
	).Scan(&bypass); err != nil {
		return fmt.Errorf("rls guard: %w", err)
	}
	if bypass && !allowBypass {
		return fmt.Errorf("%w — refusing to start (set db.allowBypassRLS=true to override in dev)", ErrRLSBypass)
	}
	return nil
}

// AuditPolicies asserts each table has RLS enabled, FORCE enabled, and a policy scoped by app.current_org_id, read either inline or through a helper function.
func AuditPolicies(ctx context.Context, conn *sql.DB, tables []string) error {
	if len(tables) == 0 {
		return nil
	}
	rlsOn, forceOn, err := loadRLSFlags(ctx, conn, tables)
	if err != nil {
		return err
	}
	policyOK, err := loadPolicyFlags(ctx, conn, tables)
	if err != nil {
		return err
	}
	audit := &RLSAuditError{}
	for _, t := range tables {
		if !rlsOn[t] {
			audit.MissingRLS = append(audit.MissingRLS, t)
		}
		if !forceOn[t] {
			audit.MissingForce = append(audit.MissingForce, t)
		}
		if !policyOK[t] {
			audit.MissingPolicy = append(audit.MissingPolicy, t)
		}
	}
	sort.Strings(audit.MissingRLS)
	sort.Strings(audit.MissingForce)
	sort.Strings(audit.MissingPolicy)
	if len(audit.MissingRLS) == 0 && len(audit.MissingForce) == 0 && len(audit.MissingPolicy) == 0 {
		return nil
	}
	return audit
}

func loadRLSFlags(ctx context.Context, conn *sql.DB, tables []string) (rls, force map[string]bool, err error) {
	rls = make(map[string]bool, len(tables))
	force = make(map[string]bool, len(tables))
	rows, err := conn.QueryContext(ctx, `
		SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p')
		  AND n.nspname = ANY (current_schemas(false))
		  AND c.relname = ANY ($1)
	`, tables)
	if err != nil {
		return nil, nil, fmt.Errorf("rls audit: pg_class: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		var relRLS, relForce bool
		if scanErr := rows.Scan(&name, &relRLS, &relForce); scanErr != nil {
			return nil, nil, fmt.Errorf("rls audit: scan pg_class: %w", scanErr)
		}
		rls[name] = relRLS
		force[name] = relForce
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, nil, fmt.Errorf("rls audit: pg_class rows: %w", rowsErr)
	}
	return rls, force, nil
}

func loadPolicyFlags(ctx context.Context, conn *sql.DB, tables []string) (map[string]bool, error) {
	markers, err := tenantScopeMarkers(ctx, conn)
	if err != nil {
		return nil, err
	}
	ok := make(map[string]bool, len(tables))
	rows, err := conn.QueryContext(ctx, `
		SELECT tablename, COALESCE(qual, '')
		FROM pg_policies
		WHERE tablename = ANY ($1)
	`, tables)
	if err != nil {
		return nil, fmt.Errorf("rls audit: pg_policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, qual string
		if scanErr := rows.Scan(&name, &qual); scanErr != nil {
			return nil, fmt.Errorf("rls audit: scan pg_policies: %w", scanErr)
		}
		if slices.ContainsFunc(markers, func(m string) bool { return strings.Contains(qual, m) }) {
			ok[name] = true
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("rls audit: pg_policies rows: %w", rowsErr)
	}
	return ok, nil
}

// NOTE: migration 002 moved the GUC read into a NULL-safe helper, so a policy's qual names that function instead of the GUC.
func tenantScopeMarkers(ctx context.Context, conn *sql.DB) ([]string, error) {
	markers := []string{currentOrgIDGUC}
	rows, err := conn.QueryContext(ctx, `
		SELECT p.proname || '('
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = ANY (current_schemas(false))
		  AND strpos(p.prosrc, $1) > 0
	`, currentOrgIDGUC)
	if err != nil {
		return nil, fmt.Errorf("rls audit: pg_proc: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var marker string
		if scanErr := rows.Scan(&marker); scanErr != nil {
			return nil, fmt.Errorf("rls audit: scan pg_proc: %w", scanErr)
		}
		markers = append(markers, marker)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("rls audit: pg_proc rows: %w", rowsErr)
	}
	return markers, nil
}
