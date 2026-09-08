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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
)

const idColumn = "id"

// WriteWorkflow mutates a writable sheet's rows and invalidates its snapshot.
type WriteWorkflow struct {
	snaps      SnapshotStore
	attempts   IdempotencyStore
	sources    Sources
	tokens     TokenSources
	reauth     Reauthers
	writers    gsheet.WriterFactory
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewWriteWorkflow binds the write path to its dependencies.
func NewWriteWorkflow(
	snaps SnapshotStore,
	attempts IdempotencyStore,
	sources Sources,
	tokens TokenSources,
	reauth Reauthers,
	writers gsheet.WriterFactory,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *WriteWorkflow {
	return &WriteWorkflow{
		snaps:      snaps,
		attempts:   attempts,
		sources:    sources,
		tokens:     tokens,
		reauth:     reauth,
		writers:    writers,
		log:        log.With("module", "sheet"),
		unexpected: unexpected,
	}
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

	var key IdempotencyKey
	reserved := false
	if idemKey != "" {
		key = IdempotencyKey{SheetID: sh.ID, Tab: tgt.tab, Key: idemKey}
		claimed, prior, rErr := w.attempts.Reserve(ctx, key, bodyHash, IdempotencyTTL)
		if rErr != nil {
			return 0, recordSpanError(span, w.unexpected(ctx, "sheet.Append: reserve attempt", rErr,
				"sheet_id", sh.ID, "tab", tgt.tab, "idempotency_key", idemKey))
		}
		if !claimed {
			n, pErr := w.replay(ctx, sh, prior, idemKey, bodyHash)
			if pErr != nil {
				return 0, recordSpanError(span, pErr)
			}
			span.SetAttributes(attribute.Bool("sheet.replayed", true))
			w.log.DebugContext(ctx, "sheet: replaying a completed write attempt",
				"sheet_id", sh.ID, "tab", tgt.tab, "idempotency_key", idemKey)
			return n, nil
		}
		reserved = true
	}

	n, err := tgt.writer.Append(ctx, tgt.src.GoogleFileID, tgt.tab, cells)
	if err != nil {
		if reserved {
			w.release(ctx, sh, key)
		}
		return 0, recordSpanError(span, w.fail(ctx, "sheet.Append: append row", err, sh, tgt.src))
	}
	if reserved {
		w.complete(ctx, sh, key, n)
	}
	w.purge(ctx, sh)
	return n, nil
}

// PatchRow overwrites the patched columns of the row sh's id column matches to id, and returns the row as written.
func (w *WriteWorkflow) PatchRow(ctx context.Context, sh *Sheet, id string, patch map[string]any) (gsheet.Row, error) {
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

	// NOTE: a fresh read, never the snapshot: merging into a stale cached row would silently discard a concurrent edit.
	tbl, err := tgt.writer.Table(ctx, tgt.src.GoogleFileID, tgt.tab)
	if err != nil {
		return nil, recordSpanError(span, w.fail(ctx, "sheet.PatchRow: read table", err, sh, tgt.src))
	}

	idCol, err := idColumnOf(tbl.Headers, tgt.tab)
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	rowIdx, err := rowIndexOf(tbl.Rows, idCol, id)
	if err != nil {
		return nil, recordSpanError(span, err)
	}
	cells, err := mergeRow(tbl.Headers, tbl.Rows[rowIdx], idCol, patch, tgt.tab)
	if err != nil {
		return nil, recordSpanError(span, err)
	}

	// NOTE: Google trims empty rows only at the tail, so rows[i] is always sheet row i+2 — one for the header row, one for 1-based indexing.
	if uErr := tgt.writer.UpdateRow(ctx, tgt.src.GoogleFileID, tgt.tab, rowIdx+2, cells); uErr != nil {
		return nil, recordSpanError(span, w.fail(ctx, "sheet.PatchRow: update row", uErr, sh, tgt.src))
	}
	w.purge(ctx, sh)
	return rowOf(tbl.Headers, cells), nil
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

func (w *WriteWorkflow) replay(ctx context.Context, sh *Sheet, prior Attempt, idemKey, bodyHash string) (int, error) {
	if prior.BodyHash != bodyHash {
		return 0, &IdempotencyMismatchError{Key: idemKey}
	}
	// NOTE: never a fabricated success — we do not know whether Google applied the earlier attempt's row.
	if !prior.Done {
		return 0, &WriteInFlightError{Key: idemKey}
	}
	var out appendResult
	if err := json.Unmarshal(prior.Payload, &out); err != nil {
		return 0, w.unexpected(ctx, "sheet.Append: decode prior attempt", err,
			"sheet_id", sh.ID, "idempotency_key", idemKey)
	}
	return out.Appended, nil
}

func (w *WriteWorkflow) complete(ctx context.Context, sh *Sheet, key IdempotencyKey, appended int) {
	payload, err := json.Marshal(appendResult{Appended: appended})
	if err != nil {
		_ = w.unexpected(ctx, "sheet.Append: serialize attempt", err,
			"sheet_id", sh.ID, "idempotency_key", key.Key)
		return
	}
	if err := w.attempts.Complete(ctx, key, payload, IdempotencyTTL); err != nil {
		_ = w.unexpected(ctx, "sheet.Append: complete attempt", err,
			"sheet_id", sh.ID, "idempotency_key", key.Key)
	}
}

// NOTE: releasing lets a genuine retry claim the key again rather than being answered forever with a stale error.
func (w *WriteWorkflow) release(ctx context.Context, sh *Sheet, key IdempotencyKey) {
	if err := w.attempts.Release(ctx, key); err != nil {
		_ = w.unexpected(ctx, "sheet.Append: release attempt", err,
			"sheet_id", sh.ID, "idempotency_key", key.Key)
	}
}

// NOTE: a purge failure is reported, not returned, because the write already happened. A read that entered ReadWorkflow.load before the write can still Put its pre-write payload after this purge, so a stale snapshot can outlive the write until its TTL.
func (w *WriteWorkflow) purge(ctx context.Context, sh *Sheet) {
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

func mergeRow(headers, row []string, idCol int, patch map[string]any, tab string) ([]any, error) {
	patched := make(map[int]any, len(patch))
	for _, name := range slices.Sorted(maps.Keys(patch)) {
		col, err := columnOf(headers, name, tab)
		if err != nil {
			return nil, err
		}
		if col == idCol {
			return nil, &ReadOnlyColumnError{Column: name}
		}
		patched[col] = patch[name]
	}

	cells := make([]any, len(headers))
	for i := range headers {
		if value, ok := patched[i]; ok {
			cells[i] = value
			continue
		}
		if i < len(row) {
			cells[i] = row[i]
			continue
		}
		cells[i] = ""
	}
	return cells, nil
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
		out[name] = cellText(cells[i])
	}
	return out
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
