package spreadsheet

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

type postgresStore struct {
	pool  pdb.Pool
	pc    *tenant.PgConn
	table *pgent.Spreadsheets
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, table: pgent.NewSpreadsheets(schema, tablePrefix)}
}

type pgSpreadsheetRow struct {
	ID           uuid.UUID `alias:"spreadsheets.id"`
	OrgID        uuid.UUID `alias:"spreadsheets.org_id"`
	ProjectID    uuid.UUID `alias:"spreadsheets.project_id"`
	CredentialID uuid.UUID `alias:"spreadsheets.credential_id"`
	GoogleFileID string    `alias:"spreadsheets.google_file_id"`
	Title        string    `alias:"spreadsheets.title"`
	Writable     bool      `alias:"spreadsheets.writable"`
	CreatedAt    time.Time `alias:"spreadsheets.created_at"`
	UpdatedAt    time.Time `alias:"spreadsheets.updated_at"`
}

func (r *pgSpreadsheetRow) toSpreadsheet() *Spreadsheet {
	return &Spreadsheet{
		ID:           r.ID,
		OrgID:        r.OrgID,
		ProjectID:    r.ProjectID,
		CredentialID: r.CredentialID,
		GoogleFileID: r.GoogleFileID,
		Title:        r.Title,
		Writable:     r.Writable,
		CreatedAt:    r.CreatedAt.UTC(),
		UpdatedAt:    r.UpdatedAt.UTC(),
	}
}

// NOTE: the spreadsheets row carries no created_by, so the resolved tenant.Context stays inside this helper.
func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, fmt.Errorf("spreadsheet.postgres: begin: %w", err)
	}
	return tx, true, nil
}

func (s *postgresStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("spreadsheet.postgres: commit: %w", cerr)
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
