package spreadsheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db    *sql.DB
	table *sqliteent.Spreadsheets
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewSpreadsheets(tablePrefix)}
}

type sqliteSpreadsheetRow struct {
	ID           string `alias:"spreadsheets.id"`
	OrgID        string `alias:"spreadsheets.org_id"`
	ProjectID    string `alias:"spreadsheets.project_id"`
	CredentialID string `alias:"spreadsheets.credential_id"`
	GoogleFileID string `alias:"spreadsheets.google_file_id"`
	Title        string `alias:"spreadsheets.title"`
	Writable     int64  `alias:"spreadsheets.writable"`
	CreatedAt    string `alias:"spreadsheets.created_at"`
	UpdatedAt    string `alias:"spreadsheets.updated_at"`
}

func (r *sqliteSpreadsheetRow) toSpreadsheet() (*Spreadsheet, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite: parse id: %w", err)
	}
	orgID, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite: parse org_id: %w", err)
	}
	projectID, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite: parse project_id: %w", err)
	}
	credentialID, err := uuid.Parse(r.CredentialID)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite: parse credential_id: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite: parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite: parse updated_at: %w", err)
	}
	return &Spreadsheet{
		ID:           id,
		OrgID:        orgID,
		ProjectID:    projectID,
		CredentialID: credentialID,
		GoogleFileID: r.GoogleFileID,
		Title:        r.Title,
		Writable:     r.Writable != 0,
		CreatedAt:    createdAt.UTC(),
		UpdatedAt:    updatedAt.UTC(),
	}, nil
}

func (s *sqliteStore) Save(ctx context.Context, sp *Spreadsheet) error {
	if _, err := tenant.From(ctx); err != nil {
		return err
	}
	writable := boolToInt(sp.Writable)
	updatedAt := sp.UpdatedAt.UTC().Format(time.RFC3339Nano)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			sp.ID.String(),
			sp.OrgID.String(),
			sp.ProjectID.String(),
			sp.CredentialID.String(),
			sp.GoogleFileID,
			sp.Title,
			writable,
			sp.CreatedAt.UTC().Format(time.RFC3339Nano),
			updatedAt,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			sqlite.SET(
				s.table.CredentialID.SET(sqlite.String(sp.CredentialID.String())),
				s.table.GoogleFileID.SET(sqlite.String(sp.GoogleFileID)),
				s.table.Title.SET(sqlite.String(sp.Title)),
				s.table.Writable.SET(sqlite.Int(writable)),
				s.table.UpdatedAt.SET(sqlite.String(updatedAt)),
			),
		)
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		if isSQLiteUniqueViolation(err) {
			return &AlreadyExistsError{
				ProjectID:    sp.ProjectID.String(),
				GoogleFileID: sp.GoogleFileID,
			}
		}
		return fmt.Errorf("spreadsheet.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Spreadsheet, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	return s.queryOne(ctx,
		s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))),
		&NotFoundError{ID: id.String()},
	)
}

func (s *sqliteStore) ByGoogleFileID(ctx context.Context, orgID, projectID uuid.UUID, fileID string) (*Spreadsheet, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return nil, &NotFoundError{ProjectID: projectID.String(), GoogleFileID: fileID}
	}
	return s.queryOne(ctx,
		s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String()))).
			AND(s.table.GoogleFileID.EQ(sqlite.String(fileID))),
		&NotFoundError{ProjectID: projectID.String(), GoogleFileID: fileID},
	)
}

func (s *sqliteStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Spreadsheet, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return []*Spreadsheet{}, nil
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String())))).
		ORDER_BY(s.table.CreatedAt.ASC())
	var rows []sqliteSpreadsheetRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("spreadsheet.sqlite.List: %w", err)
	}
	out := make([]*Spreadsheet, 0, len(rows))
	for i := range rows {
		sp, convErr := rows[i].toSpreadsheet()
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, sp)
	}
	return out, nil
}

func (s *sqliteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, s.db)
	if err != nil {
		return fmt.Errorf("spreadsheet.sqlite.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("spreadsheet.sqlite.Delete: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func (s *sqliteStore) queryOne(ctx context.Context, cond sqlite.BoolExpression, notFound *NotFoundError) (*Spreadsheet, error) {
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row sqliteSpreadsheetRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("spreadsheet.sqlite.queryOne: %w", err)
	}
	return row.toSpreadsheet()
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func isSQLiteUniqueViolation(err error) bool {
	var sqliteErr *sqlitedrv.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return true
		}
	}
	return false
}
