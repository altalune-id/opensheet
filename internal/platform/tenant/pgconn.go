package tenant

import (
	"context"
	"database/sql"
	"fmt"

	"altalune.id/template/internal/platform/db"
)

const sqlSetTenant = "SELECT set_config('app.current_org_id', $1, true)"

// PgConn wraps *sql.DB and issues transactions with the tenant-scoped app.current_org_id GUC applied.
type PgConn struct {
	DB *sql.DB
}

// NewPgConn constructs a PgConn over sqlDB.
func NewPgConn(sqlDB *sql.DB) *PgConn { return &PgConn{DB: sqlDB} }

// BeginTenanted opens a transaction and binds the tenant scope via set_config so RLS policies see the current org.
func (p *PgConn) BeginTenanted(ctx context.Context, tc Context) (*sql.Tx, error) {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, sqlSetTenant, tc.OrgID.String()); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("pgconn: set_config: %w", err)
	}
	return tx, nil
}

// RunInTx begins a tenant-scoped transaction on pc, exposes it via db.ContextWithTx, and commits when fn returns nil.
func RunInTx(ctx context.Context, pc *PgConn, tc Context, fn func(ctx context.Context) error) error {
	if _, ok := db.CurrentTx(ctx); ok {
		return db.ErrNestedUnitOfWork
	}
	if pc == nil {
		return fmt.Errorf("tenant: RunInTx: pc is nil")
	}
	tx, err := pc.BeginTenanted(ctx, tc)
	if err != nil {
		return fmt.Errorf("tenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(db.ContextWithTx(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("tenant: commit: %w", err)
	}
	return nil
}
