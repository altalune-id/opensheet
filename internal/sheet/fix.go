package sheet

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
	"altalune.id/opensheet/nanoid"
)

// idLength is the nanoid length opensheet generates a row id at.
const idLength = 21

// FixOutcome reports what AddIDColumn changed in the user's spreadsheet.
type FixOutcome struct {
	Tab           string
	Column        string
	HeaderWritten bool
	RowsFilled    int
}

// FixWorkflow adds a missing id column to a tab and backfills its values.
// NOTE: this is the only opensheet operation that writes to a spreadsheet the user did not ask it to write to, so it runs from an explicit request and never as part of publishing.
type FixWorkflow struct {
	sources    Sources
	tokens     TokenSources
	reauth     Reauthers
	writers    gsheet.WriterFactory
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewFixWorkflow binds the offer-to-fix path to its dependencies.
func NewFixWorkflow(
	sources Sources,
	tokens TokenSources,
	reauth Reauthers,
	writers gsheet.WriterFactory,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *FixWorkflow {
	return &FixWorkflow{
		sources:    sources,
		tokens:     tokens,
		reauth:     reauth,
		writers:    writers,
		log:        log.With("module", "sheet"),
		unexpected: unexpected,
	}
}

// AddIDColumn writes an id header into tab when it has none and fills an id into every row with content and no id.
func (w *FixWorkflow) AddIDColumn(ctx context.Context, spreadsheetID uuid.UUID, tab string) (FixOutcome, error) {
	ctx, span := tracer.Start(ctx, "sheet.AddIDColumn",
		trace.WithAttributes(attribute.String("spreadsheet_id", spreadsheetID.String())))
	defer span.End()

	src, err := w.sources.SourceFor(ctx, spreadsheetID)
	if err != nil {
		return FixOutcome{}, recordSpanError(span, w.passthrough(ctx, "sheet.AddIDColumn: source", err, spreadsheetID))
	}
	ts, err := w.tokens.TokenSourceFor(ctx, src.CredentialID)
	if err != nil {
		return FixOutcome{}, recordSpanError(span, w.passthrough(ctx, "sheet.AddIDColumn: token source", err, spreadsheetID))
	}
	writer, err := w.writers(ctx, ts)
	if err != nil {
		return FixOutcome{}, recordSpanError(span, w.passthrough(ctx, "sheet.AddIDColumn: build writer", err, spreadsheetID))
	}

	tab = strings.TrimSpace(tab)
	if tab == "" {
		tab, err = writer.FirstTab(ctx, src.GoogleFileID)
		if err != nil {
			return FixOutcome{}, recordSpanError(span, w.fail(ctx, "sheet.AddIDColumn: first tab", err, spreadsheetID, src))
		}
	}
	span.SetAttributes(attribute.String("sheet.tab", tab))

	tbl, err := writer.Table(ctx, src.GoogleFileID, tab)
	if err != nil {
		return FixOutcome{}, recordSpanError(span, w.fail(ctx, "sheet.AddIDColumn: read tab", err, spreadsheetID, src))
	}
	plan, err := planIDColumn(tbl, tab)
	if err != nil {
		span.RecordError(err)
		return FixOutcome{}, w.passthrough(ctx, "sheet.AddIDColumn: plan", err, spreadsheetID)
	}

	if err := writer.UpdateRange(ctx, src.GoogleFileID, tab, plan.span, plan.cells); err != nil {
		return FixOutcome{}, recordSpanError(span, w.fail(ctx, "sheet.AddIDColumn: write the id column", err, spreadsheetID, src))
	}
	w.log.InfoContext(ctx, "sheet: wrote an id column into a tab",
		"spreadsheet_id", spreadsheetID, "tab", tab, "range", plan.span,
		"header_written", plan.header, "rows_filled", plan.filled)
	return FixOutcome{Tab: tab, Column: plan.column, HeaderWritten: plan.header, RowsFilled: plan.filled}, nil
}

func (w *FixWorkflow) fail(ctx context.Context, situation string, err error, spreadsheetID uuid.UUID, src Source) error {
	if gworkspace.IsAuthExpiredError(err) {
		if mErr := w.reauth.MarkReauthNeeded(ctx, src.CredentialID); mErr != nil {
			_ = w.unexpected(ctx, situation+": mark reauth needed", mErr,
				"spreadsheet_id", spreadsheetID, "credential_id", src.CredentialID)
		}
	}
	return w.passthrough(ctx, situation, gwerr.AppError(err), spreadsheetID)
}

func (w *FixWorkflow) passthrough(ctx context.Context, situation string, err error, spreadsheetID uuid.UUID) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return w.unexpected(ctx, situation, err, "spreadsheet_id", spreadsheetID)
}

type idColumnPlan struct {
	column string
	span   string
	cells  [][]any
	header bool
	filled int
}

// NOTE: one span for the whole column, never one write per row. Cells inside the span that already hold an id are written back verbatim, which under RAW stores a numeric id as text — inside the contract, where every value is a string.
func planIDColumn(tbl gsheet.Table, tab string) (idColumnPlan, error) {
	var found []int
	for i, h := range tbl.Headers {
		if foldHeader(h) == idColumn {
			found = append(found, i)
		}
	}
	if len(found) > 1 {
		return idColumnPlan{}, &AmbiguousIDColumnError{Tab: tab, Columns: found}
	}

	header := len(found) == 0
	col := len(tbl.Headers)
	if !header {
		col = found[0]
	}
	label := gsheet.ColumnLabel(col + 1)

	// SECURITY: a claimed column is only safe to append into when it is empty — there is no undo for a write to a user's spreadsheet.
	if header {
		for i, row := range tbl.Rows {
			if cellAt(row, col) != "" {
				return idColumnPlan{}, &ColumnNotEmptyError{Tab: tab, Column: label, Row: spreadsheetRowOf(i)}
			}
		}
	}

	var need []int
	for i, row := range tbl.Rows {
		if blankRow(row) || cellAt(row, col) != "" {
			continue
		}
		need = append(need, i)
	}
	if !header && len(need) == 0 {
		return idColumnPlan{}, &NothingToFixError{Tab: tab}
	}

	first, last := 1, 1
	if !header {
		first = spreadsheetRowOf(need[0])
	}
	if len(need) > 0 {
		last = spreadsheetRowOf(need[len(need)-1])
	}

	plan := idColumnPlan{
		column: label,
		span:   fmt.Sprintf("%s%d:%s%d", label, first, label, last),
		cells:  make([][]any, 0, last-first+1),
		header: header,
		filled: len(need),
	}
	for row := first; row <= last; row++ {
		cell, err := planCell(tbl, col, row)
		if err != nil {
			return idColumnPlan{}, err
		}
		plan.cells = append(plan.cells, []any{cell})
	}
	return plan, nil
}

// NOTE: an entirely blank row is left blank, per the projection's own rule — it needs no id, and writing one would make a blank row look real.
func planCell(tbl gsheet.Table, col, row int) (string, error) {
	if row == 1 {
		return idColumn, nil
	}
	cells := tbl.Rows[row-2]
	if blankRow(cells) {
		return "", nil
	}
	if existing := cellAt(cells, col); existing != "" {
		return existing, nil
	}
	return nanoid.New(idLength)
}

// spreadsheetRowOf turns a zero-based data-row index into the 1-based spreadsheet row, past the header row.
func spreadsheetRowOf(rowIndex int) int { return rowIndex + 2 }
