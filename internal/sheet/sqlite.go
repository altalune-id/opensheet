package sheet

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
	table *sqliteent.Sheets
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewSheets(tablePrefix)}
}

type sqliteSheetRow struct {
	ID            string `alias:"sheets.id"`
	OrgID         string `alias:"sheets.org_id"`
	ProjectID     string `alias:"sheets.project_id"`
	SpreadsheetID string `alias:"sheets.spreadsheet_id"`
	Tab           string `alias:"sheets.tab"`
	Slug          string `alias:"sheets.slug"`
	Visibility    string `alias:"sheets.visibility"`
	CacheTTLSecs  int64  `alias:"sheets.cache_ttl_secs"`
	Writable      int64  `alias:"sheets.writable"`
	CreatedAt     string `alias:"sheets.created_at"`
	UpdatedAt     string `alias:"sheets.updated_at"`

	Generation     int64   `alias:"sheets.generation"`
	ValidatedAt    *string `alias:"sheets.validated_at"`
	ContractOK     int64   `alias:"sheets.contract_ok"`
	ContractReason string  `alias:"sheets.contract_reason"`
	SoftDelete     int64   `alias:"sheets.soft_delete"`
}

func (r *sqliteSheetRow) toSheet() (*Sheet, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse org_id: %w", err)
	}
	pid, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse project_id: %w", err)
	}
	ssid, err := uuid.Parse(r.SpreadsheetID)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse spreadsheet_id: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse updated_at: %w", err)
	}
	validatedAt, err := parseSQLiteTimePtr(r.ValidatedAt)
	if err != nil {
		return nil, err
	}
	return &Sheet{
		ID:            id,
		OrgID:         oid,
		ProjectID:     pid,
		SpreadsheetID: ssid,
		Tab:           r.Tab,
		Slug:          r.Slug,
		Visibility:    Visibility(r.Visibility),
		CacheTTL:      ttlFromSecs(r.CacheTTLSecs),
		Writable:      r.Writable != 0,
		CreatedAt:     createdAt.UTC(),
		UpdatedAt:     updatedAt.UTC(),

		Generation:     r.Generation,
		ValidatedAt:    validatedAt,
		ContractOK:     r.ContractOK != 0,
		ContractReason: r.ContractReason,
		SoftDelete:     r.SoftDelete != 0,
	}, nil
}

func parseSQLiteTimePtr(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil //nolint:nilnil // absent nullable timestamp
	}
	t, err := time.Parse(time.RFC3339Nano, *raw)
	if err != nil {
		return nil, fmt.Errorf("sheet.sqlite: parse validated_at: %w", err)
	}
	u := t.UTC()
	return &u, nil
}

func (s *sqliteStore) Save(ctx context.Context, sh *Sheet) error {
	if _, err := tenant.From(ctx); err != nil {
		return err
	}
	secs := secsFromTTL(sh.CacheTTL)
	writable := boolToInt(sh.Writable)
	updatedAt := sqliteent.SQLiteTime(sh.UpdatedAt)
	contractOK := boolToInt(sh.ContractOK)
	softDelete := boolToInt(sh.SoftDelete)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			sh.ID.String(),
			sh.OrgID.String(),
			sh.ProjectID.String(),
			sh.SpreadsheetID.String(),
			sh.Tab,
			sh.Slug,
			string(sh.Visibility),
			secs,
			writable,
			sqliteent.SQLiteTime(sh.CreatedAt),
			updatedAt,
			sh.Generation,
			sqliteNullableTime(sh.ValidatedAt),
			contractOK,
			sh.ContractReason,
			softDelete,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			sqlite.SET(
				s.table.Tab.SET(sqlite.String(sh.Tab)),
				s.table.Slug.SET(sqlite.String(sh.Slug)),
				s.table.Visibility.SET(sqlite.String(string(sh.Visibility))),
				s.table.CacheTTLSecs.SET(sqlite.Int(secs)),
				s.table.Writable.SET(sqlite.Int(writable)),
				s.table.UpdatedAt.SET(sqlite.String(updatedAt)),
				s.table.SoftDelete.SET(sqlite.Int(softDelete)),
			),
		)
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		if isSQLiteUniqueViolation(err) {
			return &AlreadyExistsError{Field: "slug", Value: sh.Slug}
		}
		return fmt.Errorf("sheet.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Sheet, error) {
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

func (s *sqliteStore) BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*Sheet, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	notFound := &NotFoundError{OrgID: orgID.String(), ProjectID: projectID.String(), Slug: slug}
	if tc.OrgID != orgID {
		return nil, notFound
	}
	return s.queryOne(ctx,
		s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String()))).
			AND(s.table.Slug.EQ(sqlite.String(slug))),
		notFound,
	)
}

func (s *sqliteStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Sheet, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return []*Sheet{}, nil
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String())))).
		ORDER_BY(s.table.Slug.ASC())
	var rows []sqliteSheetRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("sheet.sqlite.List: %w", err)
	}
	out := make([]*Sheet, 0, len(rows))
	for i := range rows {
		sh, cErr := rows[i].toSheet()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, sh)
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
		return fmt.Errorf("sheet.sqlite.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sheet.sqlite.Delete: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func (s *sqliteStore) queryOne(ctx context.Context, where sqlite.BoolExpression, notFound *NotFoundError) (*Sheet, error) {
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(where).
		LIMIT(1)
	var row sqliteSheetRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("sheet.sqlite.queryOne: %w", err)
	}
	return row.toSheet()
}

func sqliteNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return sqliteent.SQLiteTime(*t)
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
