package sheet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
	"altalune.id/opensheet/nanoid"
)

const (
	idColumn     = "id"
	rowIDLen     = 21
	noRowIdx     = -1
	maxBatchRows = 500
)

type rowColumns struct {
	id  int
	del int
}

// WriteWorkflow mutates a writable sheet's rows and keeps its snapshot and projection in step.
type WriteWorkflow struct {
	snaps           SnapshotStore
	rows            RowStore
	units           UnitOfWork
	attempts        IdempotencyStore
	sources         Sources
	tokens          TokenSources
	reauth          Reauthers
	writers         gsheet.WriterFactory
	defaultTTL      time.Duration
	maxPayloadBytes int64
	log             *slog.Logger
	unexpected      apperror.UnexpectedFunc
}

// NewWriteWorkflow binds the write path to its dependencies.
func NewWriteWorkflow(
	snaps SnapshotStore,
	rows RowStore,
	units UnitOfWork,
	attempts IdempotencyStore,
	sources Sources,
	tokens TokenSources,
	reauth Reauthers,
	writers gsheet.WriterFactory,
	defaultTTL time.Duration,
	maxPayloadBytes int64,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *WriteWorkflow {
	return &WriteWorkflow{
		snaps:           snaps,
		rows:            rows,
		units:           units,
		attempts:        attempts,
		sources:         sources,
		tokens:          tokens,
		reauth:          reauth,
		writers:         writers,
		defaultTTL:      defaultTTL,
		maxPayloadBytes: maxPayloadBytes,
		log:             log.With("module", "sheet"),
		unexpected:      unexpected,
	}
}

type writeOutcome struct {
	row       gsheet.Row
	id        string
	ids       []string
	liveRows  int
	wrote     bool
	projected bool
}

type rowUpdate struct {
	situation    string
	liveOnly     bool
	settled      func(row []string, cols rowColumns) bool
	precondition func(headers, row []string) error
	build        func(headers, row []string, cols rowColumns) ([]any, error)
}

type appendResult struct {
	Appended int `json:"appended"`
}

type writeTarget struct {
	writer *gsheet.Writer
	src    Source
	tab    string
}

// Append adds cells as one row at the end of sh's tab and reports how many rows Google wrote.
func (w *WriteWorkflow) Append(ctx context.Context, sh *Sheet, cells []any, idemKey, bodyHash string) (int, error) {
	ctx, span := tracer.Start(ctx, "sheet.Append",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the flag is checked before any Google call, so a non-writable sheet cannot be probed for existence through timing or error shape.
	if !sh.Writable {
		return 0, recordSpanError(span, &NotWritableError{SheetID: sh.ID.String(), Slug: sh.Slug})
	}
	if len(cells) == 0 {
		return 0, recordSpanError(span, &InvalidRowError{Reason: "no cells"})
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return 0, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	held, err := w.reserve(ctx, sh, tgt.tab, idemKey, bodyHash, "sheet.Append")
	if err != nil {
		return 0, recordSpanError(span, err)
	}
	if held.replayed {
		var prior appendResult
		if pErr := w.prior(ctx, sh, held, bodyHash, "sheet.Append", &prior); pErr != nil {
			return 0, recordSpanError(span, pErr)
		}
		span.SetAttributes(attribute.Bool("sheet.replayed", true))
		w.log.DebugContext(ctx, "sheet: replaying a completed write attempt",
			"sheet_id", sh.ID, "tab", tgt.tab, "idempotency_key", idemKey)
		return prior.Appended, nil
	}

	res, err := tgt.writer.Append(ctx, tgt.src.GoogleFileID, tgt.tab, cells)
	if err != nil {
		w.releaseHeld(ctx, sh, held)
		return 0, recordSpanError(span, w.fail(ctx, "sheet.Append: append row", err, sh, tgt.src))
	}
	w.completeHeld(ctx, sh, held, appendResult{Appended: res.Rows})
	// NOTE: 2b's keyed create supersedes this purge — it is header-aware, and takes the row_index from res.StartRow rather than refetching.
	w.purge(ctx, sh)
	return res.Rows, nil
}

// PatchRow overwrites the patched columns of the row sh's id column matches to id, and returns the row as written.
func (w *WriteWorkflow) PatchRow(
	ctx context.Context, sh *Sheet, id string, patch map[string]any, ifMatch string,
) (gsheet.Row, error) {
	ctx, span := tracer.Start(ctx, "sheet.PatchRow",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the flag is checked before any Google call, so a non-writable sheet cannot be probed for existence through timing or error shape.
	if !sh.Writable {
		return nil, recordSpanError(span, &NotWritableError{SheetID: sh.ID.String(), Slug: sh.Slug})
	}
	if len(patch) == 0 {
		return nil, recordSpanError(span, &InvalidRowError{Reason: "empty patch"})
	}

	tgt, err := w.target(ctx, sh)
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.String("sheet.tab", tgt.tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tgt.tab}
	plan := rowUpdate{
		situation:    "sheet.PatchRow",
		precondition: matchRowTag(ifMatch, id),
		build: func(headers, row []string, cols rowColumns) ([]any, error) {
			return mergeRow(headers, row, cols, patch, tgt.tab)
		},
	}
	out, err := w.applyUpdate(ctx, sh, tgt, key, id, plan)
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Bool("sheet.projected", out.projected))
	w.settle(ctx, sh, key, out)
	return out.row, nil
}

// NOTE: the sheet's write lock is held across the Google read and the Google write — the opposite of the refresh rule — because Values.Update takes no If-Match, so a check-then-write is only sound while writes to one sheet are serialized.
func (w *WriteWorkflow) applyUpdate(
	ctx context.Context, sh *Sheet, tgt writeTarget, key SnapshotKey, id string, plan rowUpdate,
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
		// NOTE: a fresh read, never the snapshot: merging into a stale cached row would silently discard a concurrent edit.
		tbl, tErr := tgt.writer.Table(ctx, tgt.src.GoogleFileID, tgt.tab)
		if tErr != nil {
			google, situation = tErr, plan.situation+": read table"
			return tErr
		}
		cols, cErr := rowColumnsOf(tbl.Headers, tgt.tab)
		if cErr != nil {
			return cErr
		}
		rowIdx, rErr := rowIndexOf(tbl.Rows, cols.id, id)
		if rErr != nil {
			return rErr
		}
		if plan.liveOnly && isTombstoneCell(cellAt(tbl.Rows[rowIdx], cols.del)) {
			return &RowNotFoundError{ID: id}
		}
		// NOTE: the fresh read is what decides a delete already applied, so a retry the projection could not answer costs no second write and keeps the first tombstone's instant.
		if plan.settled != nil && plan.settled(tbl.Rows[rowIdx], cols) {
			return nil
		}
		// NOTE: inside the lock and against the fresh read, because Values.Update takes no If-Match — outside it a check-then-write proves nothing.
		if plan.precondition != nil {
			if mErr := plan.precondition(tbl.Headers, tbl.Rows[rowIdx]); mErr != nil {
				return mErr
			}
		}
		cells, bErr := plan.build(tbl.Headers, tbl.Rows[rowIdx], cols)
		if bErr != nil {
			return bErr
		}

		// NOTE: Google trims empty rows only at the tail, so rows[i] is always sheet row i+2 — one for the header row, one for 1-based indexing.
		if uErr := tgt.writer.UpdateRow(ctx, tgt.src.GoogleFileID, tgt.tab, rowIdx+2, cells); uErr != nil {
			google, situation = uErr, plan.situation+": update row"
			return uErr
		}
		row := writtenProjection(id, rowIdx, tbl.Headers, cells, cols.del)
		out.row, out.id, out.wrote = row.Data, id, true
		out.liveRows = liveRowCount(tbl, cols.del, rowIdx, row.DeletedAt == nil)
		if pErr := w.rows.UpsertRow(txCtx, key, row); pErr != nil {
			return pErr
		}
		out.projected = true
		return nil
	})
	return w.outcome(ctx, sh, tgt, key, plan.situation, out, google, situation, runErr)
}

func (w *WriteWorkflow) outcome(
	ctx context.Context, sh *Sheet, tgt writeTarget, key SnapshotKey,
	operation string, out writeOutcome, google error, situation string, runErr error,
) (writeOutcome, error) {
	if google != nil {
		return writeOutcome{}, w.fail(ctx, situation, google, sh, tgt.src)
	}
	if runErr != nil {
		if !out.wrote {
			return writeOutcome{}, w.passthrough(ctx, operation+": write row", runErr, sh)
		}
		// NOTE: Google already holds the row, so a write-through that failed after it degrades to the purge behaviour rather than reporting a write that happened as a failure.
		_ = w.unexpected(ctx, operation+": write through", runErr, "sheet_id", sh.ID, "tab", key.Tab)
		return writeOutcome{row: out.row, id: out.id, ids: out.ids, wrote: true}, nil
	}
	return out, nil
}

func (w *WriteWorkflow) settle(ctx context.Context, sh *Sheet, key SnapshotKey, out writeOutcome) {
	if !out.projected {
		w.purge(ctx, sh)
		return
	}
	w.rebuild(ctx, sh, key, out.liveRows)
}

// NOTE: the payload is marshalled in Go, never aggregated in SQL — jsonb normalizes escapes and reorders object keys, so a SQL-side rebuild would change the ETag for rows nobody edited.
func (w *WriteWorkflow) rebuild(ctx context.Context, sh *Sheet, key SnapshotKey, liveRows int) {
	rows, err := w.rows.ListLive(ctx, key)
	if err != nil {
		_ = w.unexpected(ctx, "sheet.PatchRow: list the projected rows", err, "sheet_id", sh.ID, "tab", key.Tab)
		w.purgeSnapshot(ctx, sh)
		return
	}
	// NOTE: a projection short of the tab it mirrors — never refreshed, or missing rows added in Google since — would rebuild a truncated body, so the snapshot is purged and the next read refetches.
	if len(rows) != liveRows {
		w.log.DebugContext(ctx, "sheet: the projection does not cover the tab, so the next read refetches",
			"sheet_id", sh.ID, "tab", key.Tab, "projected_rows", len(rows), "tab_rows", liveRows)
		w.purgeSnapshot(ctx, sh)
		return
	}
	payload, err := rebuildPayload(rows)
	if err != nil {
		_ = w.unexpected(ctx, "sheet.PatchRow: rebuild the payload", err, "sheet_id", sh.ID, "tab", key.Tab)
		w.purgeSnapshot(ctx, sh)
		return
	}
	if size := int64(len(payload)); w.maxPayloadBytes > 0 && size > w.maxPayloadBytes {
		w.log.WarnContext(ctx, "sheet: the rebuilt payload exceeds the configured limit, so the next read refetches",
			"sheet_id", sh.ID, "tab", key.Tab, "bytes", size, "max_bytes", w.maxPayloadBytes)
		w.purgeSnapshot(ctx, sh)
		return
	}
	snap := Snapshot{ETag: etagOf(payload), FetchedAt: time.Now().UTC(), Payload: payload}
	if pErr := w.snaps.Put(ctx, key, snap, ttlOf(sh, w.defaultTTL)); pErr != nil {
		_ = w.unexpected(ctx, "sheet.PatchRow: snapshot put", pErr, "sheet_id", sh.ID, "tab", key.Tab)
		w.purgeSnapshot(ctx, sh)
	}
}

func (w *WriteWorkflow) target(ctx context.Context, sh *Sheet) (writeTarget, error) {
	src, err := w.sources.SourceFor(ctx, sh.SpreadsheetID)
	if err != nil {
		return writeTarget{}, w.passthrough(ctx, "sheet.write: source", err, sh)
	}
	ts, err := w.tokens.TokenSourceFor(ctx, src.CredentialID)
	if err != nil {
		return writeTarget{}, w.passthrough(ctx, "sheet.write: token source", err, sh)
	}
	writer, err := w.writers(ctx, ts)
	if err != nil {
		return writeTarget{}, w.passthrough(ctx, "sheet.write: build writer", err, sh)
	}

	tab := strings.TrimSpace(sh.Tab)
	if tab == "" {
		tab, err = writer.FirstTab(ctx, src.GoogleFileID)
		if err != nil {
			return writeTarget{}, w.fail(ctx, "sheet.write: first tab", err, sh, src)
		}
	}
	return writeTarget{writer: writer, src: src, tab: tab}, nil
}

type reservation struct {
	key      IdempotencyKey
	attempt  Attempt
	held     bool
	replayed bool
}

// NOTE: Reserve is a lock taken before the upstream call, so two simultaneous retries cannot both write; an empty key opts out and the write is unguarded.
func (w *WriteWorkflow) reserve(
	ctx context.Context, sh *Sheet, tab, idemKey, bodyHash, situation string,
) (reservation, error) {
	if idemKey == "" {
		return reservation{}, nil
	}
	held := reservation{key: IdempotencyKey{SheetID: sh.ID, Tab: tab, Key: idemKey}}
	claimed, prior, err := w.attempts.Reserve(ctx, held.key, bodyHash, IdempotencyTTL)
	if err != nil {
		return reservation{}, w.unexpected(ctx, situation+": reserve attempt", err,
			"sheet_id", sh.ID, "tab", tab, "idempotency_key", idemKey)
	}
	if !claimed {
		held.attempt, held.replayed = prior, true
		return held, nil
	}
	held.held = true
	return held, nil
}

func (w *WriteWorkflow) prior(
	ctx context.Context, sh *Sheet, held reservation, bodyHash, situation string, out any,
) error {
	if held.attempt.BodyHash != bodyHash {
		return &IdempotencyMismatchError{Key: held.key.Key}
	}
	// NOTE: never a fabricated success — we do not know whether Google applied the earlier attempt's row.
	if !held.attempt.Done {
		return &WriteInFlightError{Key: held.key.Key}
	}
	if err := json.Unmarshal(held.attempt.Payload, out); err != nil {
		return w.unexpected(ctx, situation+": decode prior attempt", err,
			"sheet_id", sh.ID, "idempotency_key", held.key.Key)
	}
	return nil
}

func (w *WriteWorkflow) completeHeld(ctx context.Context, sh *Sheet, held reservation, result any) {
	if !held.held {
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		_ = w.unexpected(ctx, "sheet.write: serialize attempt", err,
			"sheet_id", sh.ID, "idempotency_key", held.key.Key)
		return
	}
	if err := w.attempts.Complete(ctx, held.key, payload, IdempotencyTTL); err != nil {
		_ = w.unexpected(ctx, "sheet.write: complete attempt", err,
			"sheet_id", sh.ID, "idempotency_key", held.key.Key)
	}
}

// NOTE: releasing lets a genuine retry claim the key again rather than being answered forever with a stale error.
func (w *WriteWorkflow) releaseHeld(ctx context.Context, sh *Sheet, held reservation) {
	if !held.held {
		return
	}
	if err := w.attempts.Release(ctx, held.key); err != nil {
		_ = w.unexpected(ctx, "sheet.write: release attempt", err,
			"sheet_id", sh.ID, "idempotency_key", held.key.Key)
	}
}

// NOTE: a purge failure is reported, not returned, because the write already happened. A read that entered ReadWorkflow.load before the write can still Put its pre-write payload after this purge, so a stale snapshot can outlive the write until its TTL.
func (w *WriteWorkflow) purge(ctx context.Context, sh *Sheet) {
	w.purgeSnapshot(ctx, sh)
	if err := w.rows.PurgeSheet(ctx, sh.ID); err != nil {
		_ = w.unexpected(ctx, "sheet.write: purge projection", err, "sheet_id", sh.ID)
	}
}

// NOTE: the projection is left alone — it holds the row the write just committed, which is the read-your-write surface a rebuild failure must not take down.
func (w *WriteWorkflow) purgeSnapshot(ctx context.Context, sh *Sheet) {
	if err := w.snaps.PurgeSheet(ctx, sh.ID); err != nil {
		_ = w.unexpected(ctx, "sheet.write: purge snapshot", err, "sheet_id", sh.ID)
	}
}

func (w *WriteWorkflow) fail(ctx context.Context, situation string, err error, sh *Sheet, src Source) error {
	if gworkspace.IsAuthExpiredError(err) {
		if mErr := w.reauth.MarkReauthNeeded(ctx, src.CredentialID); mErr != nil {
			_ = w.unexpected(ctx, situation+": mark reauth needed", mErr,
				"sheet_id", sh.ID, "credential_id", src.CredentialID)
		}
	}
	return w.passthrough(ctx, situation, gwerr.AppError(err), sh)
}

func (w *WriteWorkflow) passthrough(ctx context.Context, situation string, err error, sh *Sheet) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return w.unexpected(ctx, situation, err, "sheet_id", sh.ID, "spreadsheet_id", sh.SpreadsheetID)
}

func recordSpanError(span trace.Span, err error) error {
	span.RecordError(err)
	return err
}

func idColumnOf(headers []string, tab string) (int, error) {
	var found []int
	for i, h := range headers {
		if foldHeader(h) == idColumn {
			found = append(found, i)
		}
	}
	if len(found) == 0 {
		return 0, &NoIDColumnError{Tab: tab}
	}
	if len(found) > 1 {
		return 0, &AmbiguousIDColumnError{Tab: tab, Columns: found}
	}
	return found[0], nil
}

func rowIndexOf(rows [][]string, idCol int, id string) (int, error) {
	match, count := 0, 0
	for i, row := range rows {
		if idCol >= len(row) || row[idCol] != id {
			continue
		}
		if count == 0 {
			match = i
		}
		count++
	}
	if count == 0 {
		return 0, &RowNotFoundError{ID: id}
	}
	if count > 1 {
		return 0, &DuplicateIDError{ID: id, Count: count}
	}
	return match, nil
}

func mergeRow(headers, row []string, cols rowColumns, patch map[string]any, tab string) ([]any, error) {
	patched, err := cols.resolve(headers, patch, tab, refuseID)
	if err != nil {
		return nil, err
	}
	return fillCells(headers, row, patched), nil
}

func replaceRowCells(headers []string, cols rowColumns, fields map[string]any, id, tab string) ([]any, error) {
	replaced, err := cols.resolve(headers, fields, tab, idMustMatch(id))
	if err != nil {
		return nil, err
	}
	replaced[cols.id] = id
	return fillCells(headers, nil, replaced), nil
}

// NOTE: RFC3339 in UTC, so the cell's text order matches chronological order the way the projection column's own encoding does.
func tombstoneRowCells(headers, row []string, cols rowColumns, slug, tab string, at time.Time) ([]any, error) {
	if cols.del < 0 {
		return nil, &SoftDeleteUnsupportedError{Slug: slug, Tab: tab}
	}
	return fillCells(headers, row, map[int]any{cols.del: at.UTC().Format(time.RFC3339)}), nil
}

func createRowCells(
	headers []string, cols rowColumns, fields map[string]any, tab string,
) (cells []any, id string, err error) {
	created, rErr := cols.resolve(headers, fields, tab, acceptID)
	if rErr != nil {
		return nil, "", rErr
	}
	id, iErr := createRowID(created, cols.id)
	if iErr != nil {
		return nil, "", iErr
	}
	created[cols.id] = id
	return fillCells(headers, nil, created), id, nil
}

func createRowID(created map[int]any, idCol int) (string, error) {
	supplied, ok := created[idCol]
	if !ok {
		id, err := nanoid.New(rowIDLen)
		if err != nil {
			return "", fmt.Errorf("sheet.write: generate a row id: %w", err)
		}
		return id, nil
	}
	id := cellText(supplied)
	if strings.TrimSpace(id) == "" {
		return "", &InvalidRowError{Reason: "the id must not be blank"}
	}
	return id, nil
}

// NOTE: a nil row clears every known column the fields omit, which is what separates a whole-row replace from a patch.
func fillCells(headers, row []string, fields map[int]any) []any {
	cells := make([]any, len(headers))
	for i := range headers {
		if value, ok := fields[i]; ok {
			cells[i] = value
			continue
		}
		if i < len(row) {
			cells[i] = row[i]
			continue
		}
		cells[i] = ""
	}
	return cells
}

func rowColumnsOf(headers []string, tab string) (rowColumns, error) {
	idCol, err := idColumnOf(headers, tab)
	if err != nil {
		return rowColumns{}, err
	}
	delCol, err := deletedAtColumnOf(headers, tab)
	if err != nil {
		return rowColumns{}, err
	}
	return rowColumns{id: idCol, del: delCol}, nil
}

// SECURITY: deleted_at is refused by column index rather than by folded name, so a header spelled "Deleted At" cannot carry a delete in as a field write and hide the row from the tab read.
func (c rowColumns) resolve(
	headers []string, fields map[string]any, tab string, onID func(name string, value any) error,
) (map[int]any, error) {
	out := make(map[int]any, len(fields))
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		col, err := columnOf(headers, name, tab)
		if err != nil {
			return nil, err
		}
		if col == c.del {
			return nil, &ReadOnlyColumnError{Column: name}
		}
		if col == c.id {
			if idErr := onID(name, fields[name]); idErr != nil {
				return nil, idErr
			}
		}
		out[col] = fields[name]
	}
	return out, nil
}

func refuseID(name string, _ any) error {
	return &ReadOnlyColumnError{Column: name}
}

func acceptID(_ string, _ any) error {
	return nil
}

func idMustMatch(id string) func(string, any) error {
	return func(_ string, value any) error {
		body := cellText(value)
		if body == id {
			return nil
		}
		return &IDMismatchError{PathID: id, BodyID: body}
	}
}

// NOTE: an absent header is last-write-wins by design, so a simple client is unaffected.
func matchRowTag(ifMatch, id string) func(headers, row []string) error {
	if strings.TrimSpace(ifMatch) == "" {
		return nil
	}
	return func(headers, row []string) error {
		tag, err := rowTagOf(headers, row)
		if err != nil {
			return err
		}
		if MatchesETag(ifMatch, tag) {
			return nil
		}
		return &PreconditionFailedError{ID: id, ETag: tag}
	}
}

// NOTE: rowOf over the fresh cells, so the tag compared here is the one a row read or a row write handed the client.
func rowTagOf(headers, cells []string) (string, error) {
	values := make([]any, len(headers))
	for i := range headers {
		values[i] = cellAt(cells, i)
	}
	raw, err := marshalRowData(rowOf(headers, values))
	if err != nil {
		return "", err
	}
	return etagOf([]byte(raw)), nil
}

func columnOf(headers []string, name, tab string) (int, error) {
	want := foldHeader(name)
	for i, h := range headers {
		if foldHeader(h) == want {
			return i, nil
		}
	}
	return 0, &UnknownColumnError{Column: name, Tab: tab}
}

// NOTE: the same normalizer the read path keys its rows with, so a PATCH response and a GET response key one row identically.
func rowOf(headers []string, cells []any) gsheet.Row {
	names, _ := gsheet.NormalizeHeaders(headers)
	out := make(gsheet.Row, len(names))
	for i, name := range names {
		if isDeletedAtHeader(headers[i]) {
			continue
		}
		out[name] = cellText(cells[i])
	}
	return out
}

func writtenProjection(id string, rowIndex int, headers []string, cells []any, delCol int) ProjectedRow {
	return ProjectedRow{
		RowID:     id,
		RowIndex:  rowIndex,
		Data:      rowOf(headers, cells),
		DeletedAt: tombstoneAt(cellTextAt(cells, delCol), time.Now().UTC()),
	}
}

func liveRowCount(tbl gsheet.Table, delCol, patchedIdx int, patchedLive bool) int {
	count := 0
	for i, cells := range tbl.Rows {
		if i == patchedIdx || blankRow(cells) || isTombstoneCell(cellAt(cells, delCol)) {
			continue
		}
		count++
	}
	if patchedLive {
		count++
	}
	return count
}

func rebuildPayload(rows []ProjectedRow) ([]byte, error) {
	return json.Marshal(liveValues(rows))
}

func cellTextAt(cells []any, col int) string {
	if col < 0 || col >= len(cells) {
		return ""
	}
	return cellText(cells[col])
}

func foldHeader(h string) string {
	return strings.ToLower(strings.TrimSpace(h))
}

func cellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return fmt.Sprint(v)
}
