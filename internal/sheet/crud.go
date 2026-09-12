package sheet

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/gwerr"
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
	state, err := w.rows.StateOf(ctx, sh.ID)
	if err != nil {
		return Row{}, recordSpanError(span, w.passthrough(ctx, "sheet.RowByID: sheet state", err, sh))
	}
	if !state.Contract.OK {
		return Row{}, recordSpanError(span, &ContractViolationError{Slug: sh.Slug, Reason: state.Contract.Reason})
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

type createResult struct {
	ID   string     `json:"id"`
	Data gsheet.Row `json:"data"`
	ETag string     `json:"etag"`
}

type batchResult struct {
	IDs []string `json:"ids"`
}

// CreateRow appends one row keyed by header, generating an id when the body names none, and returns the row as written.
func (w *WriteWorkflow) CreateRow(
	ctx context.Context, sh *Sheet, fields map[string]any, idemKey, bodyHash string,
) (WrittenRow, error) {
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

	// NOTE: a create with a server-generated id is not idempotent by construction, so a retried request duplicates the row unless the key answers it.
	held, err := w.reserve(ctx, sh, tgt.tab, idemKey, bodyHash, "sheet.CreateRow")
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	if held.replayed {
		var prior createResult
		if pErr := w.prior(ctx, sh, held, bodyHash, "sheet.CreateRow", &prior); pErr != nil {
			return WrittenRow{}, recordSpanError(span, pErr)
		}
		span.SetAttributes(attribute.Bool("sheet.replayed", true))
		w.log.DebugContext(ctx, "sheet: replaying a completed write attempt",
			"sheet_id", sh.ID, "tab", tgt.tab, "idempotency_key", idemKey)
		return WrittenRow(prior), nil
	}

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	out, err := w.applyCreate(ctx, sh, tgt, key, fields)
	if err != nil {
		w.releaseHeld(ctx, sh, held)
		return WrittenRow{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	written, err := w.writtenRowOf(ctx, sh, key, out)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	w.completeHeld(ctx, sh, held, createResult(written))
	return written, nil
}

// ReplaceRow overwrites every known column of the row id addresses, clearing those the body omits, and returns the row as written.
func (w *WriteWorkflow) ReplaceRow(
	ctx context.Context, sh *Sheet, id string, fields map[string]any, ifMatch string,
) (WrittenRow, error) {
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
	state, err := w.rows.StateOf(ctx, sh.ID)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, w.passthrough(ctx, "sheet.ReplaceRow: sheet state", err, sh))
	}
	if !state.Contract.OK {
		return WrittenRow{}, recordSpanError(span, &ContractViolationError{Slug: sh.Slug, Reason: state.Contract.Reason})
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return WrittenRow{}, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	plan := rowUpdate{
		situation:    "sheet.ReplaceRow",
		liveOnly:     true,
		precondition: matchRowTag(ifMatch, id),
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
func (w *WriteWorkflow) CreateRows(
	ctx context.Context, sh *Sheet, batch []map[string]any, idemKey, bodyHash string,
) ([]string, error) {
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

	held, err := w.reserve(ctx, sh, tgt.tab, idemKey, bodyHash, "sheet.CreateRows")
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	if held.replayed {
		var prior batchResult
		if pErr := w.prior(ctx, sh, held, bodyHash, "sheet.CreateRows", &prior); pErr != nil {
			return nil, recordSpanError(span, pErr)
		}
		span.SetAttributes(attribute.Bool("sheet.replayed", true))
		w.log.DebugContext(ctx, "sheet: replaying a completed write attempt",
			"sheet_id", sh.ID, "tab", tgt.tab, "idempotency_key", idemKey)
		return prior.IDs, nil
	}

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	out, err := w.applyBatchCreate(ctx, sh, tgt, key, batch)
	if err != nil {
		w.releaseHeld(ctx, sh, held)
		return nil, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	w.completeHeld(ctx, sh, held, batchResult{IDs: out.ids})
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
func (w *WriteWorkflow) SoftDeleteRow(ctx context.Context, sh *Sheet, id, ifMatch string) error {
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
	state, err := w.rows.StateOf(ctx, sh.ID)
	if err != nil {
		return recordSpanError(span, w.passthrough(ctx, "sheet.SoftDeleteRow: sheet state", err, sh))
	}
	if !state.Contract.OK {
		return recordSpanError(span, &ContractViolationError{Slug: sh.Slug, Reason: state.Contract.Reason})
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
		precondition: matchRowTag(ifMatch, id),
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

// RowFilter is the filtered read one request asks for, before any of it is validated.
type RowFilter struct {
	Where       []string
	Sort        []string
	Limit       string
	Cursor      string
	IfNoneMatch string
}

// FilteredRows is one page of a filtered read, the tag it revalidates against, and the cursor its next page needs.
type FilteredRows struct {
	// SECURITY: Payload is the exact bytes ETag hashes. Read it, never mutate it.
	Payload     []byte
	ETag        string
	NextCursor  string
	NotModified bool
	FetchedAt   time.Time
	Cached      bool
	Stale       bool
}

// QueryRows returns one page of the live rows of sh's tab matching f, from the projection, refreshing it first when it has gone stale.
func (w *ReadWorkflow) QueryRows(ctx context.Context, sh *Sheet, f RowFilter) (FilteredRows, error) {
	ctx, span := tracer.Start(ctx, "sheet.QueryRows",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the capability is re-checked on every read, exactly as the whole-tab read does.
	if sh.Visibility == VisibilityPublic && !w.caps.PublicSheetsEnabled() {
		return FilteredRows{}, recordSpanError(span, &PublicDisabledError{Slug: sh.Slug})
	}
	// NOTE: the window is parsed before the freshness gate so a malformed limit or cursor costs no Google call, and the clauses after it because the column list they validate against does not exist until the projection has been written.
	window, err := ParseRowWindow(f.Limit, f.Cursor, w.maxQueryRows)
	if err != nil {
		return FilteredRows{}, recordSpanError(span, err)
	}
	sort, err := ParseRowSort(f.Sort)
	if err != nil {
		return FilteredRows{}, recordSpanError(span, err)
	}
	if err = w.checkSortedWindow(sort, window); err != nil {
		return FilteredRows{}, recordSpanError(span, err)
	}
	age, err := w.freshen(ctx, sh)
	if err != nil {
		return FilteredRows{}, recordSpanError(span, err)
	}

	stats, err := w.rows.Stats(ctx, sh.ID, strings.TrimSpace(sh.Tab))
	if err != nil {
		return FilteredRows{}, recordSpanError(span, w.passthrough(ctx, "sheet.QueryRows: table stats", err, sh))
	}
	key := SnapshotKey{SheetID: sh.ID, Tab: stats.Tab}
	span.SetAttributes(attribute.String("sheet.tab", key.Tab))

	// NOTE: a tab with no live row names no column and can match no clause, so the clauses are left unvalidated rather than refused one by one — the empty page is the answer a wholly tombstoned tab must give.
	var clauses []RowClause
	if len(stats.Columns) > 0 {
		clauses, err = ParseRowClauses(f.Where, stats.Columns, key.Tab)
		if err != nil {
			return FilteredRows{}, recordSpanError(span, err)
		}
		if err = resolveRowSortColumn(sort, stats.Columns, key.Tab); err != nil {
			return FilteredRows{}, recordSpanError(span, err)
		}
	}

	// NOTE: read after the refresh, never off sh, whose generation and digest are captured when the sheet is resolved and never updated in memory.
	state, err := w.rows.StateOf(ctx, sh.ID)
	if err != nil {
		return FilteredRows{}, recordSpanError(span, w.passthrough(ctx, "sheet.QueryRows: sheet state", err, sh))
	}
	if !state.Contract.OK {
		return FilteredRows{}, recordSpanError(span,
			&ContractViolationError{Slug: sh.Slug, Reason: state.Contract.Reason})
	}
	if window.Cursor != nil && window.Cursor.Digest != state.Digest {
		return FilteredRows{}, recordSpanError(span, &StaleCursorError{Slug: sh.Slug})
	}

	q := RowQuery{Clauses: clauses, Sort: sort, Window: window}
	out := FilteredRows{
		ETag:      rowQueryETag(state.Generation, q),
		FetchedAt: age.fetchedAt,
		Cached:    age.cached,
		Stale:     age.stale,
	}
	// NOTE: evaluated after the freshness gate, because the tag hashes a generation a refresh may have moved.
	if MatchesETag(f.IfNoneMatch, out.ETag) {
		out.NotModified = true
		return out, nil
	}

	page, err := w.rows.Query(ctx, key, q)
	if err != nil {
		return FilteredRows{}, recordSpanError(span,
			w.passthrough(ctx, "sheet.QueryRows: query the projection", err, sh))
	}
	if out.Payload, err = json.Marshal(liveValues(page.Rows)); err != nil {
		return FilteredRows{}, recordSpanError(span,
			w.unexpected(ctx, "sheet.QueryRows: serialize", err, "sheet_id", sh.ID, "tab", key.Tab))
	}
	if !page.More {
		return out, nil
	}
	next, err := encodeNextRowCursor(q.Sort,
		nextRowCursor(state.Digest, q.Sort, page.Rows[len(page.Rows)-1], pagesServed(window)))
	if err != nil {
		return FilteredRows{}, recordSpanError(span,
			w.unexpected(ctx, "sheet.QueryRows: encode the next cursor", err, "sheet_id", sh.ID, "tab", key.Tab))
	}
	out.NextCursor = next
	return out, nil
}

// NOTE: the canonical spelling is stored on the sort, because both drivers use RowSort.Column verbatim as the JSON field name while the parser leaves the client's own spelling there — and the caller applies 3a's carve-out, so a tab with no live row names no column and leaves a typo'd sort unrefused exactly as it leaves a typo'd clause.
func resolveRowSortColumn(sort *RowSort, columns []string, tab string) error {
	if sort == nil {
		return nil
	}
	col, err := columnOf(columns, sort.Column, tab)
	if err != nil {
		return &UnknownSortColumnError{Column: sort.Column, Tab: tab}
	}
	sort.Column = columns[col]
	return nil
}

// NOTE: every refusal here needs no column list and no Google call, so it sits ahead of the freshness gate — placed after it, a capped request pays a refresh first and a client holding a matching tag collects a 304 instead of the refusal.
func (w *ReadWorkflow) checkSortedWindow(sort *RowSort, window RowWindow) error {
	if err := CheckRowCursorVersion(window.Cursor, sort != nil); err != nil {
		return err
	}
	if sort == nil || window.Cursor == nil {
		return nil
	}
	// NOTE: Page counts pages already served, so a cursor presenting the cap has had every page the cap allows.
	if pages := maxSortPagesOf(w.maxSortPages); window.Cursor.Page >= pages {
		return &SortDepthError{Pages: pages}
	}
	return checkRowCursorSortValue(*sort, *window.Cursor)
}

// NOTE: the null rank and the sort value are derived in Go, from the last row's own cell, because RowPage carries nothing else — and a missing key ranks null while a blank cell ranks 0 with a non-nil empty value, which is what keeps a text walk from repeating or dropping its null tail.
func nextRowCursor(digest string, sort *RowSort, last ProjectedRow, pagesServed int) RowCursor {
	out := RowCursor{Digest: digest, RowIndex: last.RowIndex}
	if sort == nil {
		return out
	}
	out.Page = pagesServed
	value, ranked := rankedSortValue(*sort, last.Data)
	if !ranked {
		out.NullRank = 1
		return out
	}
	out.SortValue = &value
	return out
}

func rankedSortValue(sort RowSort, data gsheet.Row) (string, bool) {
	raw, present := data[sort.Column]
	if !present {
		return "", false
	}
	switch sort.Hint {
	case RowHintNum:
		if _, inGrammar := ParseNum(raw); !inGrammar {
			return "", false
		}
	case RowHintDate:
		if !MatchesDateShape(raw) {
			return "", false
		}
	default:
	}
	return raw, true
}

// NOTE: EncodeRowCursor silently drops NullRank, SortValue and Page, so a sorted page must never reach it.
func encodeNextRowCursor(sort *RowSort, c RowCursor) (string, error) {
	if sort == nil {
		return EncodeRowCursor(c)
	}
	return EncodeSortedRowCursor(c)
}

// NOTE: Page counts pages already served, so the cursor issued after the first page carries 1.
func pagesServed(w RowWindow) int {
	if w.Cursor == nil {
		return 1
	}
	return w.Cursor.Page + 1
}

type projectionAge struct {
	fetchedAt time.Time
	cached    bool
	stale     bool
}

// NOTE: the gate is validated_at, never the snapshot — a write purges the snapshot and a tab over maxPayloadBytes never had one, so gating on it would mean one full Google fetch per page of a walk.
func (w *ReadWorkflow) freshen(ctx context.Context, sh *Sheet) (projectionAge, error) {
	age := projectionAge{cached: true}
	if sh.ValidatedAt != nil {
		age.fetchedAt = *sh.ValidatedAt
	}
	if !w.projectionExpired(sh) {
		return age, nil
	}
	err := w.project(ctx, sh)
	if err == nil {
		return projectionAge{fetchedAt: time.Now().UTC()}, nil
	}
	// NOTE: the projection outlives a Google outage, so a filtered read answers from it rather than failing — but only once some refresh has computed a digest, since a cursor cannot be issued without one.
	if sh.ContentDigest == "" || !isGoogleFailure(err) {
		return projectionAge{}, err
	}
	w.log.WarnContext(ctx, "sheet: querying a projection Google could not refresh",
		"sheet_id", sh.ID, "err", err)
	age.stale = true
	return age, nil
}

func (w *ReadWorkflow) projectionExpired(sh *Sheet) bool {
	// NOTE: a row write clears the digest, and a cursor can never be issued from a cleared one, so the next filtered read refreshes to recompute it.
	if sh.ValidatedAt == nil || sh.ContentDigest == "" {
		return true
	}
	return !sh.ValidatedAt.Add(ttlOf(sh, w.defaultTTL)).After(time.Now().UTC())
}

// NOTE: a distinct singleflight key, because fetch discards its type assertion — sharing the whole-tab key would hand a concurrent unfiltered caller a zero Rows, and so an empty body with no ETag.
func (w *ReadWorkflow) project(ctx context.Context, sh *Sheet) error {
	src, err := w.sources.SourceFor(ctx, sh.SpreadsheetID)
	if err != nil {
		return w.passthrough(ctx, "sheet.QueryRows: source", err, sh)
	}
	client, err := w.client(ctx, sh, src)
	if err != nil {
		return err
	}
	tab := strings.TrimSpace(sh.Tab)
	if tab == "" {
		tab, err = client.FirstTab(ctx, src.GoogleFileID)
		if err != nil {
			return w.passthrough(ctx, "sheet.QueryRows: first tab", gwerr.AppError(err), sh)
		}
	}
	key := SnapshotKey{SheetID: sh.ID, Tab: tab}
	_, err, _ = w.flight.Do("q\x00"+key.SheetID.String()+"\x00"+key.Tab, func() (any, error) {
		_, rErr := w.reproject(ctx, client, sh, src, key, false)
		return nil, rErr
	})
	if err != nil {
		w.markReauth(ctx, sh, src, err)
	}
	return err
}

// NOTE: the generation is a content-change counter after a refresh that changed nothing stops bumping it, and unlike the snapshot tag it is present even for a tab over maxPayloadBytes, which has no snapshot at all.
func rowQueryETag(generation int64, q RowQuery) string {
	clauses := make([]string, 0, len(q.Clauses))
	for _, c := range q.Clauses {
		clauses = append(clauses, c.Column+":"+rowHintPrefix(c.Hint)+string(c.Op)+":"+c.Value)
	}
	slices.Sort(clauses)
	var b strings.Builder
	b.WriteString("q\x00")
	b.WriteString(strconv.FormatInt(generation, 10))
	b.WriteString("\x00")
	b.WriteString(strconv.Itoa(q.Window.Limit))
	if q.Window.Cursor != nil {
		b.WriteString("\x00")
		b.WriteString(strconv.Itoa(q.Window.Cursor.RowIndex))
	}
	// NOTE: emitted only when a sort was asked for, and the hint prefix only when hinted, so every tag 3a already issued is byte-identical and shipping this costs no global revalidation miss.
	if q.Sort != nil {
		b.WriteString("\x00")
		b.WriteString(q.Sort.Column + ":" + rowHintPrefix(q.Sort.Hint) + rowSortDirection(*q.Sort))
	}
	for _, c := range clauses {
		b.WriteString("\x00")
		b.WriteString(c)
	}
	return etagOf([]byte(b.String()))
}
