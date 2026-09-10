package sheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

// NOTE: Postgres caps one statement at 65535 bind parameters and sheet_rows binds eight per row, so a replace inserts in chunks.
const pgRowChunk = 1000

type postgresRowStore struct {
	pgTxn

	rows   *pgent.SheetRows
	sheets *pgent.Sheets
}

func newPostgresRowStore(pc *tenant.PgConn, schema, tablePrefix string) *postgresRowStore {
	return &postgresRowStore{
		pgTxn:  pgTxn{pc: pc},
		rows:   pgent.NewSheetRows(schema, tablePrefix),
		sheets: pgent.NewSheets(schema, tablePrefix),
	}
}

type pgGenerationRow struct {
	Generation int64 `alias:"sheets.generation"`
}

type pgProjectedRow struct {
	RowID    string `alias:"sheet_rows.row_id"`
	RowIndex int64  `alias:"sheet_rows.row_index"`
	Data     string `alias:"sheet_rows.data"`
}

// NOTE: FOR UPDATE outlives this call only when the caller already has a transaction enrolled; standalone it degrades to a read of the current generation.
func (s *postgresRowStore) LockSheet(ctx context.Context, sheetID uuid.UUID) (int64, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return 0, err
	}
	gen, err := s.lockGeneration(ctx, tx, tc, sheetID)
	if err != nil {
		return 0, s.endTx(tx, owned, err)
	}
	return gen, s.endTx(tx, owned, nil)
}

func (s *postgresRowStore) Replace(
	ctx context.Context, k SnapshotKey, gen int64, rows []ProjectedRow, contract ContractState,
) (bool, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, err
	}
	values, err := pgRowValues(tc, k, rows)
	if err != nil {
		return false, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return false, err
	}
	current, err := s.lockGeneration(ctx, tx, tc, k.SheetID)
	if err != nil {
		return false, s.endTx(tx, owned, err)
	}
	if current != gen {
		return false, s.endTx(tx, owned, nil)
	}
	if err := s.deleteTab(ctx, tx, tc, k); err != nil {
		return false, s.endTx(tx, owned, err)
	}
	if err := s.insertRows(ctx, tx, values); err != nil {
		return false, s.endTx(tx, owned, err)
	}
	if err := s.commitRefresh(ctx, tx, tc, k.SheetID, contract); err != nil {
		return false, s.endTx(tx, owned, err)
	}
	return true, s.endTx(tx, owned, nil)
}

func (s *postgresRowStore) UpsertRow(ctx context.Context, k SnapshotKey, row ProjectedRow) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	data, err := marshalRowData(row.Data)
	if err != nil {
		return err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.rows.INSERT(s.rows.AllColumns).
		VALUES(
			k.SheetID, k.Tab, row.RowID, row.RowIndex, data,
			pgNullableTime(row.DeletedAt), tc.OrgID, tc.ProjectID,
		).
		ON_CONFLICT(s.rows.SheetID, s.rows.Tab, s.rows.RowID).
		DO_UPDATE(
			postgres.SET(
				s.rows.RowIndex.SET(postgres.Int(int64(row.RowIndex))),
				s.rows.Data.SET(pgJSONB(data)),
				s.rows.DeletedAt.SET(pgNullableTimestampz(row.DeletedAt)),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.rows.postgres.UpsertRow: %w", execErr))
	}
	if err := s.bumpGeneration(ctx, tx, tc, k.SheetID); err != nil {
		return s.endTx(tx, owned, err)
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresRowStore) ListLive(ctx context.Context, k SnapshotKey) ([]ProjectedRow, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.rows.RowID, s.rows.RowIndex, s.rows.Data).
		FROM(s.rows).
		WHERE(s.rows.SheetID.EQ(postgres.UUID(k.SheetID)).
			AND(s.rows.Tab.EQ(postgres.String(k.Tab))).
			AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID))).
			AND(s.rows.DeletedAt.IS_NULL())).
		ORDER_BY(s.rows.RowIndex.ASC())
	var scanned []pgProjectedRow
	if qErr := stmt.QueryContext(ctx, tx, &scanned); qErr != nil {
		return nil, fmt.Errorf("sheet.rows.postgres.ListLive: %w", qErr)
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

func (s *postgresRowStore) PurgeSheet(ctx context.Context, sheetID uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.rows.DELETE().
		WHERE(s.rows.SheetID.EQ(postgres.UUID(sheetID)).
			AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.rows.postgres.PurgeSheet: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresRowStore) lockGeneration(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID,
) (int64, error) {
	stmt := postgres.SELECT(s.sheets.Generation).
		FROM(s.sheets).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1).
		FOR(postgres.UPDATE())
	var row pgGenerationRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return 0, &NotFoundError{ID: sheetID.String()}
		}
		return 0, fmt.Errorf("sheet.rows.postgres: lock sheet: %w", err)
	}
	return row.Generation, nil
}

func (s *postgresRowStore) deleteTab(ctx context.Context, tx *sql.Tx, tc tenant.Context, k SnapshotKey) error {
	stmt := s.rows.DELETE().
		WHERE(s.rows.SheetID.EQ(postgres.UUID(k.SheetID)).
			AND(s.rows.Tab.EQ(postgres.String(k.Tab))).
			AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.postgres: delete tab: %w", err)
	}
	return nil
}

func (s *postgresRowStore) insertRows(ctx context.Context, tx *sql.Tx, values [][]any) error {
	for start := 0; start < len(values); start += pgRowChunk {
		end := min(start+pgRowChunk, len(values))
		stmt := s.rows.INSERT(s.rows.AllColumns)
		for _, v := range values[start:end] {
			stmt = stmt.VALUES(v[0], v[1:]...)
		}
		if _, err := stmt.ExecContext(ctx, tx); err != nil {
			return fmt.Errorf("sheet.rows.postgres: insert rows: %w", err)
		}
	}
	return nil
}

func (s *postgresRowStore) bumpGeneration(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID,
) error {
	stmt := s.sheets.UPDATE(s.sheets.Generation).
		SET(s.sheets.Generation.ADD(postgres.Int(1))).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.postgres: bump generation: %w", err)
	}
	return nil
}

func (s *postgresRowStore) commitRefresh(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID, contract ContractState,
) error {
	stmt := s.sheets.UPDATE(
		s.sheets.Generation, s.sheets.ValidatedAt, s.sheets.ContractOK, s.sheets.ContractReason,
	).
		SET(
			s.sheets.Generation.ADD(postgres.Int(1)),
			postgres.TimestampzT(time.Now().UTC()),
			postgres.Bool(contract.OK),
			postgres.String(contract.Reason),
		).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.postgres: commit refresh: %w", err)
	}
	return nil
}

func pgRowValues(tc tenant.Context, k SnapshotKey, rows []ProjectedRow) ([][]any, error) {
	out := make([][]any, 0, len(rows))
	for i := range rows {
		data, err := marshalRowData(rows[i].Data)
		if err != nil {
			return nil, err
		}
		out = append(out, []any{
			k.SheetID, k.Tab, rows[i].RowID, rows[i].RowIndex, data,
			pgNullableTime(rows[i].DeletedAt), tc.OrgID, tc.ProjectID,
		})
	}
	return out, nil
}

// NOTE: postgres.String renders $n::text, which a jsonb column rejects in an assignment, so the placeholder is cast to jsonb instead.
func pgJSONB(raw string) postgres.StringExpression {
	return postgres.StringExp(postgres.CAST(postgres.String(raw)).AS("jsonb"))
}

func pgNullableTimestampz(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return postgres.TimestampzExp(postgres.NULL)
	}
	return postgres.TimestampzT(t.UTC())
}
