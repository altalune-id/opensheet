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

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteRowStore struct {
	db     *sql.DB
	rows   *sqliteent.SheetRows
	sheets *sqliteent.Sheets
}

func newSQLiteRowStore(sqlDB *sql.DB, tablePrefix string) *sqliteRowStore {
	return &sqliteRowStore{
		db:     sqlDB,
		rows:   sqliteent.NewSheetRows(tablePrefix),
		sheets: sqliteent.NewSheets(tablePrefix),
	}
}

type sqliteProjectedRow struct {
	RowID     string  `alias:"sheet_rows.row_id"`
	RowIndex  int64   `alias:"sheet_rows.row_index"`
	Data      string  `alias:"sheet_rows.data"`
	DeletedAt *string `alias:"sheet_rows.deleted_at"`
}

type sqliteSheetStateRow struct {
	OK            int64  `alias:"sheets.contract_ok"`
	Reason        string `alias:"sheets.contract_reason"`
	SoftDelete    int64  `alias:"sheets.soft_delete"`
	ContentDigest string `alias:"sheets.content_digest"`
	Generation    int64  `alias:"sheets.generation"`
}

type sqliteRowStats struct {
	Tab        string `alias:"sheet_rows.tab"`
	Total      int64  `alias:"row_stats.total"`
	Tombstoned int64  `alias:"row_stats.tombstoned"`
}

type sqliteGenerationRow struct {
	Generation    int64  `alias:"sheets.generation"`
	ContentDigest string `alias:"sheets.content_digest"`
}

// NOTE: sqlite is single-writer, so a read of the generation already has the exclusivity FOR UPDATE buys on postgres.
func (s *sqliteRowStore) LockSheet(ctx context.Context, sheetID uuid.UUID) (int64, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	var gen int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		g, _, gErr := s.generation(ctx, tx, tc, sheetID)
		if gErr != nil {
			return gErr
		}
		gen = g
		return nil
	})
	if err != nil {
		return 0, err
	}
	return gen, nil
}

func (s *sqliteRowStore) Replace(
	ctx context.Context, k SnapshotKey, gen int64, rows []ProjectedRow, contract ContractState,
) (bool, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, err
	}
	values, err := sqliteRowValues(tc, k, rows)
	if err != nil {
		return false, err
	}
	digest, err := RowsDigest(rows)
	if err != nil {
		return false, err
	}
	applied := false
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		current, stored, gErr := s.generation(ctx, tx, tc, k.SheetID)
		if gErr != nil {
			return gErr
		}
		if current != gen {
			return nil
		}
		if dErr := s.deleteTab(ctx, tx, tc, k); dErr != nil {
			return dErr
		}
		if iErr := s.insertRows(ctx, tx, values); iErr != nil {
			return iErr
		}
		if uErr := s.commitRefresh(ctx, tx, tc, k.SheetID, contract, digest, digest != stored); uErr != nil {
			return uErr
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *sqliteRowStore) MarkContract(
	ctx context.Context, sheetID uuid.UUID, gen int64, contract ContractState,
) (bool, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, err
	}
	applied := false
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		current, _, gErr := s.generation(ctx, tx, tc, sheetID)
		if gErr != nil {
			return gErr
		}
		if current != gen {
			return nil
		}
		if mErr := s.markContract(ctx, tx, tc, sheetID, contract); mErr != nil {
			return mErr
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

func (s *sqliteRowStore) UpsertRow(ctx context.Context, k SnapshotKey, row ProjectedRow) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	data, err := marshalRowData(row.Data)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt := s.rows.INSERT(s.rows.AllColumns).
			VALUES(
				k.SheetID.String(), k.Tab, row.RowID, int64(row.RowIndex), data,
				sqliteNullableTime(row.DeletedAt), tc.OrgID.String(), tc.ProjectID.String(),
			).
			ON_CONFLICT(s.rows.SheetID, s.rows.Tab, s.rows.RowID).
			DO_UPDATE(
				sqlite.SET(
					s.rows.RowIndex.SET(sqlite.Int(int64(row.RowIndex))),
					s.rows.Data.SET(sqlite.String(data)),
					s.rows.DeletedAt.SET(sqliteNullableTimeExpr(row.DeletedAt)),
				),
			)
		if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
			return fmt.Errorf("sheet.rows.sqlite.UpsertRow: %w", execErr)
		}
		return s.bumpGeneration(ctx, tx, tc, k.SheetID)
	})
}

// NOTE: the tombstone travels with the row — create must refuse a tombstoned id and delete must tell a tombstone from an unknown id, so neither can be served by a live-only lookup.
func (s *sqliteRowStore) RowByID(ctx context.Context, k SnapshotKey, rowID string) (ProjectedRow, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return ProjectedRow{}, err
	}
	var out ProjectedRow
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		stmt := sqlite.SELECT(s.rows.RowID, s.rows.RowIndex, s.rows.Data, s.rows.DeletedAt).
			FROM(s.rows).
			WHERE(s.rows.SheetID.EQ(sqlite.String(k.SheetID.String())).
				AND(s.rows.Tab.EQ(sqlite.String(k.Tab))).
				AND(s.rows.RowID.EQ(sqlite.String(rowID))).
				AND(s.rows.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
			LIMIT(1)
		var scanned sqliteProjectedRow
		if qErr := stmt.QueryContext(ctx, tx, &scanned); qErr != nil {
			if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
				return &RowNotFoundError{ID: rowID}
			}
			return fmt.Errorf("sheet.rows.sqlite.RowByID: %w", qErr)
		}
		data, dErr := unmarshalRowData(scanned.Data)
		if dErr != nil {
			return dErr
		}
		deletedAt, tErr := sqliteTombstone(scanned.DeletedAt)
		if tErr != nil {
			return tErr
		}
		out = ProjectedRow{
			RowID:     scanned.RowID,
			RowIndex:  int(scanned.RowIndex),
			Data:      data,
			DeletedAt: deletedAt,
		}
		return nil
	})
	if err != nil {
		return ProjectedRow{}, err
	}
	return out, nil
}

func (s *sqliteRowStore) StateOf(ctx context.Context, sheetID uuid.UUID) (SheetState, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return SheetState{}, err
	}
	var out SheetState
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		stmt := sqlite.SELECT(
			s.sheets.ContractOK, s.sheets.ContractReason, s.sheets.SoftDelete,
			s.sheets.ContentDigest, s.sheets.Generation,
		).
			FROM(s.sheets).
			WHERE(s.sheets.ID.EQ(sqlite.String(sheetID.String())).
				AND(s.sheets.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
			LIMIT(1)
		var scanned sqliteSheetStateRow
		if qErr := stmt.QueryContext(ctx, tx, &scanned); qErr != nil {
			if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
				return &NotFoundError{ID: sheetID.String()}
			}
			return fmt.Errorf("sheet.rows.sqlite.StateOf: %w", qErr)
		}
		out = SheetState{
			Contract: ContractState{
				OK: scanned.OK != 0, Reason: scanned.Reason, SoftDelete: scanned.SoftDelete != 0,
			},
			Digest:     scanned.ContentDigest,
			Generation: scanned.Generation,
		}
		return nil
	})
	if err != nil {
		return SheetState{}, err
	}
	return out, nil
}

func (s *sqliteRowStore) ListLive(ctx context.Context, k SnapshotKey) ([]ProjectedRow, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	stmt := sqlite.SELECT(s.rows.RowID, s.rows.RowIndex, s.rows.Data).
		FROM(s.rows).
		WHERE(s.rows.SheetID.EQ(sqlite.String(k.SheetID.String())).
			AND(s.rows.Tab.EQ(sqlite.String(k.Tab))).
			AND(s.rows.OrgID.EQ(sqlite.String(tc.OrgID.String()))).
			AND(s.rows.DeletedAt.IS_NULL())).
		ORDER_BY(s.rows.RowIndex.ASC())
	var scanned []sqliteProjectedRow
	if qErr := stmt.QueryContext(ctx, s.db, &scanned); qErr != nil {
		return nil, fmt.Errorf("sheet.rows.sqlite.ListLive: %w", qErr)
	}
	out := make([]ProjectedRow, 0, len(scanned))
	for i := range scanned {
		data, dErr := unmarshalRowData(scanned[i].Data)
		if dErr != nil {
			return nil, dErr
		}
		out = append(out, ProjectedRow{
			RowID:    scanned[i].RowID,
			RowIndex: int(scanned[i].RowIndex),
			Data:     data,
		})
	}
	return out, nil
}

// NOTE: an empty sheets.tab means "the first tab", whose name only Google knows — so the busiest projected tab stands in, which a rename's orphans mirror row for row.
func (s *sqliteRowStore) Stats(ctx context.Context, sheetID uuid.UUID, tab string) (TableStats, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return TableStats{}, err
	}
	where := s.rows.SheetID.EQ(sqlite.String(sheetID.String())).
		AND(s.rows.OrgID.EQ(sqlite.String(tc.OrgID.String())))
	if tab != "" {
		where = where.AND(s.rows.Tab.EQ(sqlite.String(tab)))
	}
	counted := sqlite.SELECT(
		s.rows.Tab,
		sqlite.COUNT(sqlite.STAR).AS("row_stats.total"),
		sqlite.COUNT(s.rows.DeletedAt).AS("row_stats.tombstoned"),
	).
		FROM(s.rows).
		WHERE(where).
		GROUP_BY(s.rows.Tab).
		ORDER_BY(sqlite.COUNT(sqlite.STAR).DESC(), s.rows.Tab.ASC()).
		LIMIT(1)
	var stats sqliteRowStats
	if qErr := counted.QueryContext(ctx, s.db, &stats); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return TableStats{Tab: tab}, nil
		}
		return TableStats{}, fmt.Errorf("sheet.rows.sqlite.Stats: %w", qErr)
	}
	columns, err := s.firstLiveColumns(ctx, tc, SnapshotKey{SheetID: sheetID, Tab: stats.Tab})
	if err != nil {
		return TableStats{}, err
	}
	return TableStats{
		Tab:      stats.Tab,
		Columns:  columns,
		RowCount: stats.Total - stats.Tombstoned,
	}, nil
}

func (s *sqliteRowStore) firstLiveColumns(ctx context.Context, tc tenant.Context, k SnapshotKey) ([]string, error) {
	stmt := sqlite.SELECT(s.rows.Data).
		FROM(s.rows).
		WHERE(s.rows.SheetID.EQ(sqlite.String(k.SheetID.String())).
			AND(s.rows.Tab.EQ(sqlite.String(k.Tab))).
			AND(s.rows.OrgID.EQ(sqlite.String(tc.OrgID.String()))).
			AND(s.rows.DeletedAt.IS_NULL())).
		ORDER_BY(s.rows.RowIndex.ASC()).
		LIMIT(1)
	var row sqliteProjectedRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sheet.rows.sqlite: first live row: %w", err)
	}
	data, err := unmarshalRowData(row.Data)
	if err != nil {
		return nil, err
	}
	return dataColumns(data), nil
}

func (s *sqliteRowStore) PurgeSheet(ctx context.Context, sheetID uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt := s.rows.DELETE().
			WHERE(s.rows.SheetID.EQ(sqlite.String(sheetID.String())).
				AND(s.rows.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
		if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
			return fmt.Errorf("sheet.rows.sqlite.PurgeSheet: %w", execErr)
		}
		return nil
	})
}

func (s *sqliteRowStore) generation(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID,
) (gen int64, digest string, err error) {
	stmt := sqlite.SELECT(s.sheets.Generation, s.sheets.ContentDigest).
		FROM(s.sheets).
		WHERE(s.sheets.ID.EQ(sqlite.String(sheetID.String())).
			AND(s.sheets.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		LIMIT(1)
	var row sqliteGenerationRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return 0, "", &NotFoundError{ID: sheetID.String()}
		}
		return 0, "", fmt.Errorf("sheet.rows.sqlite: read generation: %w", err)
	}
	return row.Generation, row.ContentDigest, nil
}

func (s *sqliteRowStore) deleteTab(ctx context.Context, tx *sql.Tx, tc tenant.Context, k SnapshotKey) error {
	stmt := s.rows.DELETE().
		WHERE(s.rows.SheetID.EQ(sqlite.String(k.SheetID.String())).
			AND(s.rows.Tab.EQ(sqlite.String(k.Tab))).
			AND(s.rows.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.sqlite: delete tab: %w", err)
	}
	return nil
}

func (s *sqliteRowStore) insertRows(ctx context.Context, tx *sql.Tx, values [][]any) error {
	for _, v := range values {
		stmt := s.rows.INSERT(s.rows.AllColumns).VALUES(v[0], v[1:]...)
		if _, err := stmt.ExecContext(ctx, tx); err != nil {
			return fmt.Errorf("sheet.rows.sqlite: insert rows: %w", err)
		}
	}
	return nil
}

func (s *sqliteRowStore) bumpGeneration(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID,
) error {
	// NOTE: clearing the digest is what refuses every outstanding cursor — '' is a sentinel RowsDigest never returns, so the next refresh is guaranteed to bump.
	stmt := s.sheets.UPDATE(s.sheets.Generation, s.sheets.ContentDigest).
		SET(s.sheets.Generation.ADD(sqlite.Int(1)), sqlite.String("")).
		WHERE(s.sheets.ID.EQ(sqlite.String(sheetID.String())).
			AND(s.sheets.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.sqlite: bump generation: %w", err)
	}
	return nil
}

// NOTE: the generation moves only when the digest moved — a refresh that refetched identical rows must not invalidate an outstanding cursor or an If-None-Match.
func (s *sqliteRowStore) commitRefresh(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID,
	contract ContractState, digest string, bump bool,
) error {
	sets := []any{
		s.sheets.ValidatedAt.SET(sqlite.String(sqliteent.SQLiteTime(time.Now()))),
		s.sheets.ContractOK.SET(sqlite.Int(boolToInt(contract.OK))),
		s.sheets.ContractReason.SET(sqlite.String(contract.Reason)),
		s.sheets.SoftDelete.SET(sqlite.Int(boolToInt(contract.SoftDelete))),
		s.sheets.ContentDigest.SET(sqlite.String(digest)),
	}
	if bump {
		sets = append(sets, s.sheets.Generation.SET(s.sheets.Generation.ADD(sqlite.Int(1))))
	}
	stmt := s.sheets.UPDATE().
		SET(sets[0], sets[1:]...).
		WHERE(s.sheets.ID.EQ(sqlite.String(sheetID.String())).
			AND(s.sheets.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.sqlite: commit refresh: %w", err)
	}
	return nil
}

// NOTE: generation is left alone — the projected rows did not change, so a concurrent refresh has nothing to discard.
func (s *sqliteRowStore) markContract(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID, contract ContractState,
) error {
	stmt := s.sheets.UPDATE(
		s.sheets.ValidatedAt, s.sheets.ContractOK, s.sheets.ContractReason, s.sheets.SoftDelete,
	).
		SET(
			sqlite.String(sqliteent.SQLiteTime(time.Now())),
			sqlite.Int(boolToInt(contract.OK)),
			sqlite.String(contract.Reason),
			sqlite.Int(boolToInt(contract.SoftDelete)),
		).
		WHERE(s.sheets.ID.EQ(sqlite.String(sheetID.String())).
			AND(s.sheets.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.sqlite: mark contract: %w", err)
	}
	return nil
}

func (s *sqliteRowStore) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return fn(tx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sheet.rows.sqlite: begin: %w", err)
	}
	if fnErr := fn(tx); fnErr != nil {
		_ = tx.Rollback()
		return fnErr
	}
	if cErr := tx.Commit(); cErr != nil {
		return fmt.Errorf("sheet.rows.sqlite: commit: %w", cErr)
	}
	return nil
}

func sqliteRowValues(tc tenant.Context, k SnapshotKey, rows []ProjectedRow) ([][]any, error) {
	out := make([][]any, 0, len(rows))
	for i := range rows {
		data, err := marshalRowData(rows[i].Data)
		if err != nil {
			return nil, err
		}
		out = append(out, []any{
			k.SheetID.String(), k.Tab, rows[i].RowID, int64(rows[i].RowIndex), data,
			sqliteNullableTime(rows[i].DeletedAt), tc.OrgID.String(), tc.ProjectID.String(),
		})
	}
	return out, nil
}

func sqliteTombstone(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil //nolint:nilnil // absent nullable timestamp
	}
	at, err := time.Parse(time.RFC3339Nano, *raw)
	if err != nil {
		return nil, fmt.Errorf("sheet.rows.sqlite: parse deleted_at: %w", err)
	}
	utc := at.UTC()
	return &utc, nil
}

func sqliteNullableTimeExpr(t *time.Time) sqlite.StringExpression {
	if t == nil {
		return sqlite.StringExp(sqlite.NULL)
	}
	return sqlite.String(sqliteent.SQLiteTime(*t))
}
