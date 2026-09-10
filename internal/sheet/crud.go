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

// NOTE: an empty sheets.tab names the first tab, whose name only Google knows, so the busiest projected tab stands in — the resolution Stats already makes.
func (w *ReadWorkflow) rowKey(ctx context.Context, sh *Sheet) (SnapshotKey, error) {
	tab := strings.TrimSpace(sh.Tab)
	if tab != "" {
		return SnapshotKey{SheetID: sh.ID, Tab: tab}, nil
	}
	stats, err := w.rows.Stats(ctx, sh.ID, "")
	if err != nil {
		return SnapshotKey{}, w.unexpected(ctx, "sheet.RowByID: resolve the tab", err, "sheet_id", sh.ID)
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
