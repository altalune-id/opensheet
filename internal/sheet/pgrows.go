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
	Generation    int64  `alias:"sheets.generation"`
	ContentDigest string `alias:"sheets.content_digest"`
}

type pgRowStats struct {
	Tab        string `alias:"sheet_rows.tab"`
	Total      int64  `alias:"row_stats.total"`
	Tombstoned int64  `alias:"row_stats.tombstoned"`
}

type pgProjectedRow struct {
	RowID     string     `alias:"sheet_rows.row_id"`
	RowIndex  int64      `alias:"sheet_rows.row_index"`
	Data      string     `alias:"sheet_rows.data"`
	DeletedAt *time.Time `alias:"sheet_rows.deleted_at"`
}

type pgSheetStateRow struct {
	OK            bool   `alias:"sheets.contract_ok"`
	Reason        string `alias:"sheets.contract_reason"`
	SoftDelete    bool   `alias:"sheets.soft_delete"`
	ContentDigest string `alias:"sheets.content_digest"`
	Generation    int64  `alias:"sheets.generation"`
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
	gen, _, err := s.lockGeneration(ctx, tx, tc, sheetID)
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
	digest, err := RowsDigest(rows)
	if err != nil {
		return false, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return false, err
	}
	current, stored, err := s.lockGeneration(ctx, tx, tc, k.SheetID)
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
	if err := s.commitRefresh(ctx, tx, tc, k.SheetID, contract, digest, digest != stored); err != nil {
		return false, s.endTx(tx, owned, err)
	}
	return true, s.endTx(tx, owned, nil)
}

func (s *postgresRowStore) MarkContract(
	ctx context.Context, sheetID uuid.UUID, gen int64, contract ContractState,
) (bool, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return false, err
	}
	current, _, err := s.lockGeneration(ctx, tx, tc, sheetID)
	if err != nil {
		return false, s.endTx(tx, owned, err)
	}
	if current != gen {
		return false, s.endTx(tx, owned, nil)
	}
	if err := s.markContract(ctx, tx, tc, sheetID, contract); err != nil {
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

// NOTE: the tombstone travels with the row — create must refuse a tombstoned id and delete must tell a tombstone from an unknown id, so neither can be served by a live-only lookup.
func (s *postgresRowStore) RowByID(ctx context.Context, k SnapshotKey, rowID string) (ProjectedRow, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return ProjectedRow{}, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return ProjectedRow{}, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.rows.RowID, s.rows.RowIndex, s.rows.Data, s.rows.DeletedAt).
		FROM(s.rows).
		WHERE(s.rows.SheetID.EQ(postgres.UUID(k.SheetID)).
			AND(s.rows.Tab.EQ(postgres.String(k.Tab))).
			AND(s.rows.RowID.EQ(postgres.String(rowID))).
			AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1)
	var scanned pgProjectedRow
	if qErr := stmt.QueryContext(ctx, tx, &scanned); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return ProjectedRow{}, &RowNotFoundError{ID: rowID}
		}
		return ProjectedRow{}, fmt.Errorf("sheet.rows.postgres.RowByID: %w", qErr)
	}
	data, err := unmarshalRowData(scanned.Data)
	if err != nil {
		return ProjectedRow{}, err
	}
	return ProjectedRow{
		RowID:     scanned.RowID,
		RowIndex:  int(scanned.RowIndex),
		Data:      data,
		DeletedAt: utcOrNil(scanned.DeletedAt),
	}, nil
}

func (s *postgresRowStore) StateOf(ctx context.Context, sheetID uuid.UUID) (SheetState, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return SheetState{}, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return SheetState{}, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(
		s.sheets.ContractOK, s.sheets.ContractReason, s.sheets.SoftDelete,
		s.sheets.ContentDigest, s.sheets.Generation,
	).
		FROM(s.sheets).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1)
	var scanned pgSheetStateRow
	if qErr := stmt.QueryContext(ctx, tx, &scanned); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return SheetState{}, &NotFoundError{ID: sheetID.String()}
		}
		return SheetState{}, fmt.Errorf("sheet.rows.postgres.StateOf: %w", qErr)
	}
	return SheetState{
		Contract:   ContractState{OK: scanned.OK, Reason: scanned.Reason, SoftDelete: scanned.SoftDelete},
		Digest:     scanned.ContentDigest,
		Generation: scanned.Generation,
	}, nil
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

func (s *postgresRowStore) Query(ctx context.Context, k SnapshotKey, q RowQuery) (RowPage, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return RowPage{}, err
	}
	where, err := s.queryPredicate(tc, k, q)
	if err != nil {
		return RowPage{}, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return RowPage{}, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	limit := rowQueryLimit(q.Window)
	stmt := postgres.SELECT(s.rows.RowID, s.rows.RowIndex, s.rows.Data).
		FROM(s.rows).
		WHERE(where).
		ORDER_BY(s.rows.RowIndex.ASC()).
		LIMIT(int64(limit) + 1)
	var scanned []pgProjectedRow
	if qErr := stmt.QueryContext(ctx, tx, &scanned); qErr != nil {
		return RowPage{}, fmt.Errorf("sheet.rows.postgres.Query: %w", qErr)
	}
	out := make([]ProjectedRow, 0, len(scanned))
	for i := range scanned {
		data, dErr := unmarshalRowData(scanned[i].Data)
		if dErr != nil {
			return RowPage{}, dErr
		}
		out = append(out, ProjectedRow{
			RowID:    scanned[i].RowID,
			RowIndex: int(scanned[i].RowIndex),
			Data:     data,
		})
	}
	return rowPageOf(out, limit), nil
}

// NOTE: the keyset reads sheet_rows_page_idx (sheet_id, tab, row_index) directly; OFFSET would re-scan from the top and skip rows a concurrent edit shifted.
func (s *postgresRowStore) queryPredicate(
	tc tenant.Context, k SnapshotKey, q RowQuery,
) (postgres.BoolExpression, error) {
	where := s.rows.SheetID.EQ(postgres.UUID(k.SheetID)).
		AND(s.rows.Tab.EQ(postgres.String(k.Tab))).
		AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID))).
		AND(s.rows.DeletedAt.IS_NULL())
	if q.Window.Cursor != nil {
		where = where.AND(s.rows.RowIndex.GT(postgres.Int(int64(q.Window.Cursor.RowIndex))))
	}
	for _, clause := range q.Clauses {
		pred, err := pgClausePredicate(s.rows.Data, clause)
		if err != nil {
			return nil, err
		}
		where = where.AND(pred)
	}
	return where, nil
}

// NOTE: the field name binds as a parameter, and that bind is the injection boundary — column validation exists for the UnknownColumnError refusal, not for safety.
func pgCell(data postgres.ColumnString, field string) postgres.StringExpression {
	return postgres.StringExp(postgres.CustomExpression(
		data, postgres.Token("->>"), postgres.String(field)))
}

// NOTE: postgres.CAST emits no parentheses over ->>, which renders data ->> $1::text::numeric and fails with "operator does not exist: jsonb ->> numeric", so the guards parenthesise the extraction by hand.
func pgCellParens(data postgres.ColumnString, field string) postgres.Expression {
	return postgres.CustomExpression(
		postgres.Token("("), data, postgres.Token("->>"), postgres.String(field), postgres.Token(")"))
}

// NOTE: the CASE is required rather than defensive — ORDER BY and the SELECT list have no guard to reorder against, where a bare cast raises on the first unparseable cell. NumPattern alone is not the grammar either: it bounds each digit run to 15, not the significant digits across the point, so the length test carries the rest. pg_input_is_valid is not the guard — it admits NaN, which outranks every finite numeric.
func pgNumCell(data postgres.ColumnString, field string) postgres.FloatExpression {
	return postgres.FloatExp(postgres.CustomExpression(
		postgres.Token("(CASE WHEN"), pgCellParens(data, field),
		postgres.Token("~"), postgres.String(NumPattern),
		postgres.Token("AND length(ltrim(translate("), pgCellParens(data, field),
		postgres.Token(", '-.', ''), '0')) <="), postgres.Int(NumSignificanceLimit),
		postgres.Token("THEN"), pgCellParens(data, field),
		postgres.Token("::numeric END)"),
	))
}

// NOTE: no cast and no ::date — every field of the date grammar is fixed-width and ordered most significant first under one timezone, so lexical order is chronological and the shape guard is the whole implementation.
func pgDateCell(data postgres.ColumnString, field string) postgres.StringExpression {
	return postgres.StringExp(postgres.CustomExpression(
		postgres.Token("(CASE WHEN"), pgCellParens(data, field),
		postgres.Token("~"), postgres.String(DatePattern),
		postgres.Token("THEN"), pgCellParens(data, field),
		postgres.Token("END)"),
	))
}

func pgClausePredicate(data postgres.ColumnString, c RowClause) (postgres.BoolExpression, error) {
	switch c.Hint {
	case RowHintNum:
		return pgNumPredicate(pgNumCell(data, c.Column), c)
	case RowHintDate:
		return pgTextPredicate(pgDateCell(data, c.Column), c)
	}
	return pgTextPredicate(pgCell(data, c.Column), c)
}

// NOTE: the operand binds as a Go string with no cast of its own — pgx sends OID 0 and the server infers numeric from the comparison, and inside the 15-significant-digit bound numeric and float64 hold the same value.
func pgNumPredicate(cell postgres.FloatExpression, c RowClause) (postgres.BoolExpression, error) {
	operand := postgres.Decimal(c.Value)
	switch c.Op {
	case RowOpEq:
		return cell.EQ(operand), nil
	case RowOpNe:
		return cell.NOT_EQ(operand), nil
	case RowOpGt:
		return cell.GT(operand), nil
	case RowOpGte:
		return cell.GT_EQ(operand), nil
	case RowOpLt:
		return cell.LT(operand), nil
	case RowOpLte:
		return cell.LT_EQ(operand), nil
	}
	return nil, unsupportedRowOp(c.Op)
}

func pgTextPredicate(cell postgres.StringExpression, c RowClause) (postgres.BoolExpression, error) {
	switch c.Op {
	case RowOpEq:
		return cell.EQ(postgres.String(c.Value)), nil
	case RowOpNe:
		return cell.NOT_EQ(postgres.String(c.Value)), nil
	case RowOpGt:
		return cell.GT(postgres.String(c.Value)), nil
	case RowOpGte:
		return cell.GT_EQ(postgres.String(c.Value)), nil
	case RowOpLt:
		return cell.LT(postgres.String(c.Value)), nil
	case RowOpLte:
		return cell.LT_EQ(postgres.String(c.Value)), nil
	case RowOpContains, RowOpStarts:
		return pgLike(cell, c.Pattern), nil
	case RowOpIn:
		return cell.IN(pgStringList(c.Values)...), nil
	case RowOpEmpty:
		return cell.IS_NULL().OR(cell.EQ(postgres.String(""))), nil
	case RowOpPresent:
		return cell.IS_NOT_NULL().AND(cell.NOT_EQ(postgres.String(""))), nil
	}
	return nil, unsupportedRowOp(c.Op)
}

// NOTE: go-jet's LIKE takes only a pattern, so ESCAPE is emitted by hand — Postgres defaults to backslash but SQLite has no default, and an unescaped pattern turns a literal % into a wildcard.
func pgLike(cell postgres.StringExpression, pattern string) postgres.BoolExpression {
	return postgres.BoolExp(postgres.CustomExpression(
		postgres.Token("("),
		postgres.LOWER(cell),
		postgres.Token("LIKE"),
		postgres.LOWER(postgres.String(pattern)),
		postgres.Token("ESCAPE"),
		postgres.String(RowLikeEscape),
		postgres.Token(")"),
	))
}

func pgStringList(values []string) []postgres.Expression {
	out := make([]postgres.Expression, 0, len(values))
	for _, v := range values {
		out = append(out, postgres.String(v))
	}
	return out
}

// NOTE: an empty sheets.tab means "the first tab", whose name only Google knows — so the busiest projected tab stands in, which a rename's orphans mirror row for row.
func (s *postgresRowStore) Stats(ctx context.Context, sheetID uuid.UUID, tab string) (TableStats, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return TableStats{}, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return TableStats{}, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	where := s.rows.SheetID.EQ(postgres.UUID(sheetID)).
		AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID)))
	if tab != "" {
		where = where.AND(s.rows.Tab.EQ(postgres.String(tab)))
	}
	counted := postgres.SELECT(
		s.rows.Tab,
		postgres.COUNT(postgres.STAR).AS("row_stats.total"),
		postgres.COUNT(s.rows.DeletedAt).AS("row_stats.tombstoned"),
	).
		FROM(s.rows).
		WHERE(where).
		GROUP_BY(s.rows.Tab).
		ORDER_BY(postgres.COUNT(postgres.STAR).DESC(), s.rows.Tab.ASC()).
		LIMIT(1)
	var stats pgRowStats
	if qErr := counted.QueryContext(ctx, tx, &stats); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return TableStats{Tab: tab}, nil
		}
		return TableStats{}, fmt.Errorf("sheet.rows.postgres.Stats: %w", qErr)
	}
	columns, err := s.firstLiveColumns(ctx, tx, tc, SnapshotKey{SheetID: sheetID, Tab: stats.Tab})
	if err != nil {
		return TableStats{}, err
	}
	return TableStats{
		Tab:      stats.Tab,
		Columns:  columns,
		RowCount: stats.Total - stats.Tombstoned,
	}, nil
}

func (s *postgresRowStore) firstLiveColumns(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, k SnapshotKey,
) ([]string, error) {
	stmt := postgres.SELECT(s.rows.Data).
		FROM(s.rows).
		WHERE(s.rows.SheetID.EQ(postgres.UUID(k.SheetID)).
			AND(s.rows.Tab.EQ(postgres.String(k.Tab))).
			AND(s.rows.OrgID.EQ(postgres.UUID(tc.OrgID))).
			AND(s.rows.DeletedAt.IS_NULL())).
		ORDER_BY(s.rows.RowIndex.ASC()).
		LIMIT(1)
	var row pgProjectedRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sheet.rows.postgres: first live row: %w", err)
	}
	data, err := unmarshalRowData(row.Data)
	if err != nil {
		return nil, err
	}
	return dataColumns(data), nil
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
) (gen int64, digest string, err error) {
	stmt := postgres.SELECT(s.sheets.Generation, s.sheets.ContentDigest).
		FROM(s.sheets).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1).
		FOR(postgres.UPDATE())
	var row pgGenerationRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return 0, "", &NotFoundError{ID: sheetID.String()}
		}
		return 0, "", fmt.Errorf("sheet.rows.postgres: lock sheet: %w", err)
	}
	return row.Generation, row.ContentDigest, nil
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
	// NOTE: clearing the digest is what refuses every outstanding cursor — '' is a sentinel RowsDigest never returns, so the next refresh is guaranteed to bump.
	stmt := s.sheets.UPDATE(s.sheets.Generation, s.sheets.ContentDigest).
		SET(s.sheets.Generation.ADD(postgres.Int(1)), postgres.String("")).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.postgres: bump generation: %w", err)
	}
	return nil
}

// NOTE: the generation moves only when the digest moved — a refresh that refetched identical rows must not invalidate an outstanding cursor or an If-None-Match.
func (s *postgresRowStore) commitRefresh(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID,
	contract ContractState, digest string, bump bool,
) error {
	sets := []any{
		s.sheets.ValidatedAt.SET(postgres.TimestampzT(time.Now().UTC())),
		s.sheets.ContractOK.SET(postgres.Bool(contract.OK)),
		s.sheets.ContractReason.SET(postgres.String(contract.Reason)),
		s.sheets.SoftDelete.SET(postgres.Bool(contract.SoftDelete)),
		s.sheets.ContentDigest.SET(postgres.String(digest)),
	}
	if bump {
		sets = append(sets, s.sheets.Generation.SET(s.sheets.Generation.ADD(postgres.Int(1))))
	}
	stmt := s.sheets.UPDATE().
		SET(sets[0], sets[1:]...).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.postgres: commit refresh: %w", err)
	}
	return nil
}

// NOTE: generation is left alone — the projected rows did not change, so a concurrent refresh has nothing to discard.
func (s *postgresRowStore) markContract(
	ctx context.Context, tx *sql.Tx, tc tenant.Context, sheetID uuid.UUID, contract ContractState,
) error {
	stmt := s.sheets.UPDATE(
		s.sheets.ValidatedAt, s.sheets.ContractOK, s.sheets.ContractReason, s.sheets.SoftDelete,
	).
		SET(
			postgres.TimestampzT(time.Now().UTC()),
			postgres.Bool(contract.OK),
			postgres.String(contract.Reason),
			postgres.Bool(contract.SoftDelete),
		).
		WHERE(s.sheets.ID.EQ(postgres.UUID(sheetID)).
			AND(s.sheets.OrgID.EQ(postgres.UUID(tc.OrgID))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("sheet.rows.postgres: mark contract: %w", err)
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
