package sheet

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/gworkspace/gsheet"
)

// Row is one row of a published tab, carrying the cache provenance and the tag a conditional read revalidates against.
type Row struct {
	Data gsheet.Row
	// SECURITY: Payload is the exact bytes ETag hashes. Read it, never mutate it.
	Payload   []byte
	ETag      string
	FetchedAt time.Time
	Cached    bool
	Stale     bool
}

// RowByID returns one live row of sh's tab from the projection, refreshing an expired snapshot first.
func (w *ReadWorkflow) RowByID(ctx context.Context, sh *Sheet, id string) (Row, error) {
	ctx, span := tracer.Start(ctx, "sheet.RowByID",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// NOTE: the whole-tab read carries the capability check, the freshness rule and the singleflight, so a row read is never staler than a tab read of the same sheet.
	tab, err := w.Rows(ctx, sh)
	if err != nil {
		return Row{}, recordSpanError(span, err)
	}

	// NOTE: read after the refresh, never off sh — under drift the refresh skips projection, and sh.ContractOK is the pre-refresh verdict.
	contract, err := w.rows.ContractOf(ctx, sh.ID)
	if err != nil {
		return Row{}, recordSpanError(span, w.passthrough(ctx, "sheet.RowByID: contract state", err, sh))
	}
	if !contract.OK {
		return Row{}, recordSpanError(span, &ContractViolationError{Slug: sh.Slug, Reason: contract.Reason})
	}

	key, err := w.rowKey(ctx, sh)
	if err != nil {
		return Row{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", key.Tab))

	row, err := w.rows.RowByID(ctx, key, id)
	if err != nil {
		return Row{}, recordSpanError(span, w.passthrough(ctx, "sheet.RowByID: read the projected row", err, sh))
	}
	if row.DeletedAt != nil {
		return Row{}, recordSpanError(span, &RowNotFoundError{ID: id})
	}

	out, err := w.rowOf(ctx, sh, key, row, tab)
	if err != nil {
		return Row{}, recordSpanError(span, err)
	}
	return out, nil
}

func (w *ReadWorkflow) rowKey(ctx context.Context, sh *Sheet) (SnapshotKey, error) {
	key, err := projectedKey(ctx, w.rows, sh)
	if err != nil {
		return SnapshotKey{}, w.unexpected(ctx, "sheet.RowByID: resolve the tab", err, "sheet_id", sh.ID)
	}
	return key, nil
}

// NOTE: an empty sheets.tab names the first tab, whose name only Google knows, so the busiest projected tab stands in — the resolution Stats already makes.
func projectedKey(ctx context.Context, rows RowStore, sh *Sheet) (SnapshotKey, error) {
	tab := strings.TrimSpace(sh.Tab)
	if tab != "" {
		return SnapshotKey{SheetID: sh.ID, Tab: tab}, nil
	}
	stats, err := rows.Stats(ctx, sh.ID, "")
	if err != nil {
		return SnapshotKey{}, err
	}
	return SnapshotKey{SheetID: sh.ID, Tab: stats.Tab}, nil
}

// NOTE: the tag is the payload hash of one row, taken with etagOf, so a row tag and a tab tag cannot drift apart.
func (w *ReadWorkflow) rowOf(
	ctx context.Context, sh *Sheet, key SnapshotKey, row ProjectedRow, tab Rows,
) (Row, error) {
	raw, err := marshalRowData(row.Data)
	if err != nil {
		return Row{}, w.unexpected(ctx, "sheet.RowByID: serialize", err, "sheet_id", sh.ID, "tab", key.Tab)
	}
	payload := []byte(raw)
	return Row{
		Data:      row.Data,
		Payload:   payload,
		ETag:      etagOf(payload),
		FetchedAt: tab.FetchedAt,
		Cached:    tab.Cached,
		Stale:     tab.Stale,
	}, nil
}

// WrittenRow is one row as a write left it, carrying the tag a later conditional read revalidates against.
type WrittenRow struct {
	ID   string
	Data gsheet.Row
	ETag string
}

// CreateRow appends one row keyed by header, generating an id when the body names none, and returns the row as written.
func (w *WriteWorkflow) CreateRow(ctx context.Context, sh *Sheet, fields map[string]any) (WrittenRow, error) {
	ctx, span := tracer.Start(ctx, "sheet.CreateRow",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the flag is checked before any Google call, so a non-writable sheet cannot be probed for existence through timing or error shape.
	if !sh.Writable {
		return WrittenRow{}, recordSpanError(span, &NotWritableError{SheetID: sh.ID.String(), Slug: sh.Slug})
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	out, err := w.applyCreate(ctx, sh, tgt, key, fields)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	written, err := w.writtenRowOf(ctx, sh, key, out)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	return written, nil
}

// ReplaceRow overwrites every known column of the row id addresses, clearing those the body omits, and returns the row as written.
func (w *WriteWorkflow) ReplaceRow(ctx context.Context, sh *Sheet, id string, fields map[string]any) (WrittenRow, error) {
	ctx, span := tracer.Start(ctx, "sheet.ReplaceRow",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the flag is checked before any Google call, so a non-writable sheet cannot be probed for existence through timing or error shape.
	if !sh.Writable {
		return WrittenRow{}, recordSpanError(span, &NotWritableError{SheetID: sh.ID.String(), Slug: sh.Slug})
	}
	contract, err := w.rows.ContractOf(ctx, sh.ID)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, w.passthrough(ctx, "sheet.ReplaceRow: contract state", err, sh))
	}
	if !contract.OK {
		return WrittenRow{}, recordSpanError(span, &ContractViolationError{Slug: sh.Slug, Reason: contract.Reason})
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	plan := rowUpdate{
		situation: "sheet.ReplaceRow",
		liveOnly:  true,
		build: func(headers, _ []string, cols rowColumns) ([]any, error) {
			return replaceRowCells(headers, cols, fields, id, tgt.tab)
		},
	}
	out, err := w.applyUpdate(ctx, sh, tgt, key, id, plan)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	written, err := w.writtenRowOf(ctx, sh, key, out)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	return written, nil
}

func (w *WriteWorkflow) applyCreate(
	ctx context.Context, sh *Sheet, tgt writeTarget, key SnapshotKey, fields map[string]any,
) (writeOutcome, error) {
	var (
		out       writeOutcome
		google    error
		situation string
	)
	runErr := w.units.Run(ctx, func(txCtx context.Context) error {
		if _, lErr := w.rows.LockSheet(txCtx, sh.ID); lErr != nil {
			return lErr
		}
		// NOTE: a fresh read, never the projection: the header row maps the body's keys to cell positions, and the tab is what an id must be unique within.
		tbl, tErr := tgt.writer.Table(ctx, tgt.src.GoogleFileID, tgt.tab)
		if tErr != nil {
			google, situation = tErr, "sheet.CreateRow: read table"
			return tErr
		}
		cols, cErr := rowColumnsOf(tbl.Headers, tgt.tab)
		if cErr != nil {
			return cErr
		}
		cells, id, bErr := createCells(tbl, cols, tgt.tab, fields)
		if bErr != nil {
			return bErr
		}

		res, aErr := tgt.writer.Append(ctx, tgt.src.GoogleFileID, tgt.tab, cells)
		if aErr != nil {
			google, situation = aErr, "sheet.CreateRow: append row"
			return aErr
		}
		row := writtenProjection(id, res.StartRow-2, tbl.Headers, cells, cols.del)
		out.row, out.id, out.wrote = row.Data, id, true
		out.liveRows = liveRowCount(tbl, cols.del, noRowIdx, true)
		if row.RowIndex < 0 {
			return &InvalidRowError{Reason: "the appended row landed above the first data row"}
		}
		if pErr := w.rows.UpsertRow(txCtx, key, row); pErr != nil {
			return pErr
		}
		out.projected = true
		return nil
	})
	return w.outcome(ctx, sh, tgt, key, "sheet.CreateRow", out, google, situation, runErr)
}

// CreateRows appends every row of a batch in one request, refusing the whole batch when any row is invalid, and returns the ids in request order.
func (w *WriteWorkflow) CreateRows(ctx context.Context, sh *Sheet, batch []map[string]any) ([]string, error) {
	ctx, span := tracer.Start(ctx, "sheet.CreateRows",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
			attribute.Int("sheet.rows", len(batch)),
		))
	defer span.End()

	// SECURITY: the flag is checked before any Google call, so a non-writable sheet cannot be probed for existence through timing or error shape.
	if !sh.Writable {
		return nil, recordSpanError(span, &NotWritableError{SheetID: sh.ID.String(), Slug: sh.Slug})
	}
	if len(batch) == 0 {
		return nil, recordSpanError(span, &InvalidRowError{Reason: "no rows"})
	}
	if len(batch) > maxBatchRows {
		return nil, recordSpanError(span, &BatchTooLargeError{Rows: len(batch), Limit: maxBatchRows})
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	out, err := w.applyBatchCreate(ctx, sh, tgt, key, batch)
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	return out.ids, nil
}

func (w *WriteWorkflow) applyBatchCreate(
	ctx context.Context, sh *Sheet, tgt writeTarget, key SnapshotKey, batch []map[string]any,
) (writeOutcome, error) {
	var (
		out       writeOutcome
		google    error
		situation string
	)
	runErr := w.units.Run(ctx, func(txCtx context.Context) error {
		if _, lErr := w.rows.LockSheet(txCtx, sh.ID); lErr != nil {
			return lErr
		}
		// NOTE: a fresh read, never the projection: the header row maps each body's keys to cell positions, and the tab is what an id must be unique within.
		tbl, tErr := tgt.writer.Table(ctx, tgt.src.GoogleFileID, tgt.tab)
		if tErr != nil {
			google, situation = tErr, "sheet.CreateRows: read table"
			return tErr
		}
		cols, cErr := rowColumnsOf(tbl.Headers, tgt.tab)
		if cErr != nil {
			return cErr
		}
		// NOTE: the whole batch is validated before the append, so one bad row refuses the request with nothing written.
		rows, ids, bErr := createBatchCells(tbl, cols, tgt.tab, batch)
		if bErr != nil {
			return bErr
		}

		// NOTE: one Values.Append carrying every row — all-or-nothing at the same granularity as a single write, and its reported range spans the whole block, so each row_index follows from the start row.
		res, aErr := tgt.writer.AppendRows(ctx, tgt.src.GoogleFileID, tgt.tab, rows)
		if aErr != nil {
			google, situation = aErr, "sheet.CreateRows: append rows"
			return aErr
		}
		out.ids, out.wrote = ids, true
		out.liveRows = liveRowCount(tbl, cols.del, noRowIdx, false) + len(rows)
		if res.StartRow-2 < 0 {
			return &InvalidRowError{Reason: "the appended block landed above the first data row"}
		}
		for i, cells := range rows {
			row := writtenProjection(ids[i], res.StartRow-2+i, tbl.Headers, cells, cols.del)
			if pErr := w.rows.UpsertRow(txCtx, key, row); pErr != nil {
				return pErr
			}
		}
		out.projected = true
		return nil
	})
	return w.outcome(ctx, sh, tgt, key, "sheet.CreateRows", out, google, situation, runErr)
}

// SoftDeleteRow tombstones the row id addresses, reporting success for a row already tombstoned.
func (w *WriteWorkflow) SoftDeleteRow(ctx context.Context, sh *Sheet, id string) error {
	ctx, span := tracer.Start(ctx, "sheet.SoftDeleteRow",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the flag is checked before any Google call, so a non-writable sheet cannot be probed for existence through timing or error shape.
	if !sh.Writable {
		return recordSpanError(span, &NotWritableError{SheetID: sh.ID.String(), Slug: sh.Slug})
	}
	// NOTE: the persisted header flag, so a tab that cannot record a deletion is refused before any Google call — the case capabilities.softDelete warns a client about.
	if !sh.SoftDelete {
		return recordSpanError(span, &SoftDeleteUnsupportedError{Slug: sh.Slug, Tab: strings.TrimSpace(sh.Tab)})
	}
	contract, err := w.rows.ContractOf(ctx, sh.ID)
	if err != nil {
		return recordSpanError(span, w.passthrough(ctx, "sheet.SoftDeleteRow: contract state", err, sh))
	}
	if !contract.OK {
		return recordSpanError(span, &ContractViolationError{Slug: sh.Slug, Reason: contract.Reason})
	}

	// NOTE: the tombstone check precedes every precondition, because a retry of a delete that already
	// landed carries a tag no live row matches and idempotency answers it before any of them.
	tombstoned, err := w.tombstoned(ctx, sh, id)
	if err != nil {
		return recordSpanError(span, err)
	}
	if tombstoned {
		span.SetAttributes(attribute.Bool("sheet.tombstoned", true))
		return nil
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	plan := rowUpdate{
		situation: "sheet.SoftDeleteRow",
		settled: func(row []string, cols rowColumns) bool {
			return isTombstoneCell(cellAt(row, cols.del))
		},
		build: func(headers, row []string, cols rowColumns) ([]any, error) {
			return tombstoneRowCells(headers, row, cols, sh.Slug, tgt.tab, time.Now())
		},
	}
	out, err := w.applyUpdate(ctx, sh, tgt, key, id, plan)
	if err != nil {
		return recordSpanError(span, err)
	}
	if !out.wrote {
		return nil
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	return nil
}

// NOTE: a projection short of the tab it mirrors cannot report absence, so an unresolved id falls through to the fresh read inside the lock, which is what refuses an unknown one.
func (w *WriteWorkflow) tombstoned(ctx context.Context, sh *Sheet, id string) (bool, error) {
	key, err := projectedKey(ctx, w.rows, sh)
	if err != nil {
		return false, w.unexpected(ctx, "sheet.SoftDeleteRow: resolve the tab", err, "sheet_id", sh.ID)
	}
	row, err := w.rows.RowByID(ctx, key, id)
	if IsRowNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, w.passthrough(ctx, "sheet.SoftDeleteRow: read the projected row", err, sh)
	}
	return row.DeletedAt != nil, nil
}

// NOTE: the tag is the payload hash of one row, taken with etagOf, so a written row and a read row cannot tag the same bytes differently.
func (w *WriteWorkflow) writtenRowOf(
	ctx context.Context, sh *Sheet, key SnapshotKey, out writeOutcome,
) (WrittenRow, error) {
	raw, err := marshalRowData(out.row)
	if err != nil {
		return WrittenRow{}, w.unexpected(ctx, "sheet.write: serialize the row", err,
			"sheet_id", sh.ID, "tab", key.Tab)
	}
	return WrittenRow{ID: out.id, Data: out.row, ETag: etagOf([]byte(raw))}, nil
}

func createCells(
	tbl gsheet.Table, cols rowColumns, tab string, fields map[string]any,
) (cells []any, id string, err error) {
	cells, id, err = createRowCells(tbl.Headers, cols, fields, tab)
	if err != nil {
		return nil, "", err
	}
	// NOTE: the tab, not the projection — a projection short of the tab it mirrors would let a second row carry an id the tab already holds, which every later write to that id then trips over.
	if dErr := refuseTakenID(tbl.Rows, cols.id, id); dErr != nil {
		return nil, "", dErr
	}
	return cells, id, nil
}

func createBatchCells(
	tbl gsheet.Table, cols rowColumns, tab string, batch []map[string]any,
) (rows [][]any, ids []string, err error) {
	rows = make([][]any, 0, len(batch))
	ids = make([]string, 0, len(batch))
	seen := make(map[string]int, len(batch))
	for _, fields := range batch {
		cells, id, cErr := createCells(tbl, cols, tab, fields)
		if cErr != nil {
			return nil, nil, cErr
		}
		seen[id]++
		if seen[id] > 1 {
			return nil, nil, &DuplicateIDError{ID: id, Count: seen[id]}
		}
		rows = append(rows, cells)
		ids = append(ids, id)
	}
	return rows, ids, nil
}

func refuseTakenID(rows [][]string, idCol int, id string) error {
	count := 0
	for _, row := range rows {
		if cellAt(row, idCol) == id {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	return &DuplicateIDError{ID: id, Count: count}
}
