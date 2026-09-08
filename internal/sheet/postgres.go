package sheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

const pgUniqueViolation = "23505"

type pgTxn struct {
	pc *tenant.PgConn
}

type postgresStore struct {
	pgTxn

	pool  pdb.Pool
	table *pgent.Sheets
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pgTxn: pgTxn{pc: pc}, pool: pool, table: pgent.NewSheets(schema, tablePrefix)}
}

type pgSheetRow struct {
	ID            uuid.UUID `alias:"sheets.id"`
	OrgID         uuid.UUID `alias:"sheets.org_id"`
	ProjectID     uuid.UUID `alias:"sheets.project_id"`
	SpreadsheetID uuid.UUID `alias:"sheets.spreadsheet_id"`
	Tab           string    `alias:"sheets.tab"`
	Slug          string    `alias:"sheets.slug"`
	Visibility    string    `alias:"sheets.visibility"`
	CacheTTLSecs  int64     `alias:"sheets.cache_ttl_secs"`
	CreatedAt     time.Time `alias:"sheets.created_at"`
	UpdatedAt     time.Time `alias:"sheets.updated_at"`
}

func (r *pgSheetRow) toSheet() *Sheet {
	return &Sheet{
		ID:            r.ID,
		OrgID:         r.OrgID,
		ProjectID:     r.ProjectID,
		SpreadsheetID: r.SpreadsheetID,
		Tab:           r.Tab,
		Slug:          r.Slug,
		Visibility:    Visibility(r.Visibility),
		CacheTTL:      ttlFromSecs(r.CacheTTLSecs),
		CreatedAt:     r.CreatedAt.UTC(),
		UpdatedAt:     r.UpdatedAt.UTC(),
	}
}

func (s pgTxn) txAcquire(ctx context.Context) (*sql.Tx, bool, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, fmt.Errorf("sheet.postgres: begin: %w", err)
	}
	return tx, true, nil
}

func (s pgTxn) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("sheet.postgres: commit: %w", cerr)
	}
	return nil
}

func isPgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgUniqueViolation
	}
	return false
}
