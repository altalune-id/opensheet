package sheet

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
)

const deletedAtColumn = "deleted_at"

// PublishWorkflow reads a tab and decides whether it may be published as a table.
type PublishWorkflow struct {
	sources    Sources
	tokens     TokenSources
	reauth     Reauthers
	clients    gsheet.Factory
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewPublishWorkflow binds the publish path to its dependencies.
func NewPublishWorkflow(
	sources Sources,
	tokens TokenSources,
	reauth Reauthers,
	clients gsheet.Factory,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *PublishWorkflow {
	return &PublishWorkflow{
		sources:    sources,
		tokens:     tokens,
		reauth:     reauth,
		clients:    clients,
		log:        log.With("module", "sheet"),
		unexpected: unexpected,
	}
}

// Validate reads the tab and reports whether it satisfies the table contract.
func (w *PublishWorkflow) Validate(ctx context.Context, spreadsheetID uuid.UUID, tab string) (ContractState, []string, error) {
	ctx, span := tracer.Start(ctx, "sheet.Validate",
		trace.WithAttributes(attribute.String("spreadsheet_id", spreadsheetID.String())))
	defer span.End()

	src, err := w.sources.SourceFor(ctx, spreadsheetID)
	if err != nil {
		return ContractState{}, nil, recordSpanError(span, w.passthrough(ctx, "sheet.Validate: source", err, spreadsheetID))
	}
	ts, err := w.tokens.TokenSourceFor(ctx, src.CredentialID)
	if err != nil {
		return ContractState{}, nil, recordSpanError(span, w.passthrough(ctx, "sheet.Validate: token source", err, spreadsheetID))
	}
	client, err := w.clients(ctx, ts)
	if err != nil {
		return ContractState{}, nil, recordSpanError(span, w.passthrough(ctx, "sheet.Validate: build client", err, spreadsheetID))
	}

	tab = strings.TrimSpace(tab)
	if tab == "" {
		tab, err = client.FirstTab(ctx, src.GoogleFileID)
		if err != nil {
			return ContractState{}, nil, recordSpanError(span, w.fail(ctx, "sheet.Validate: first tab", err, spreadsheetID, src))
		}
	}
	span.SetAttributes(attribute.String("sheet.tab", tab))

	tbl, err := client.Table(ctx, src.GoogleFileID, tab)
	if err != nil {
		return ContractState{}, nil, recordSpanError(span, w.fail(ctx, "sheet.Validate: read tab", err, spreadsheetID, src))
	}

	state, headers, vErr := validateContract(tbl, tab)
	if vErr != nil {
		span.RecordError(vErr)
		w.log.InfoContext(ctx, "sheet: tab does not satisfy the table contract",
			"spreadsheet_id", spreadsheetID, "tab", tab, "reason", state.Reason)
	}
	span.SetAttributes(attribute.Bool("sheet.contract_ok", state.OK))
	return state, headers, vErr
}

func (w *PublishWorkflow) fail(ctx context.Context, situation string, err error, spreadsheetID uuid.UUID, src Source) error {
	if gworkspace.IsAuthExpiredError(err) {
		if mErr := w.reauth.MarkReauthNeeded(ctx, src.CredentialID); mErr != nil {
			_ = w.unexpected(ctx, situation+": mark reauth needed", mErr,
				"spreadsheet_id", spreadsheetID, "credential_id", src.CredentialID)
		}
	}
	return w.passthrough(ctx, situation, gwerr.AppError(err), spreadsheetID)
}

// NOTE: a Google or credential failure already carries a wire code and is an expected outcome the surfaces map, so it passes through instead of being reported as an incident.
func (w *PublishWorkflow) passthrough(ctx context.Context, situation string, err error, spreadsheetID uuid.UUID) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return w.unexpected(ctx, situation, err, "spreadsheet_id", spreadsheetID)
}

func validateContract(tbl gsheet.Table, tab string) (ContractState, []string, error) {
	headers := slices.Clone(tbl.Headers)

	idCol, err := idColumnOf(headers, tab)
	if err != nil {
		return contractFailed(err), headers, err
	}
	if _, dErr := deletedAtColumnOf(headers, tab); dErr != nil {
		return contractFailed(dErr), headers, dErr
	}

	counts := make(map[string]int, len(tbl.Rows))
	order := make([]string, 0, len(tbl.Rows))
	for i, row := range tbl.Rows {
		if blankRow(row) {
			continue
		}
		id := cellAt(row, idCol)
		if id == "" {
			eErr := &EmptyIDError{Tab: tab, RowIndex: i}
			return contractFailed(eErr), headers, eErr
		}
		if counts[id] == 0 {
			order = append(order, id)
		}
		counts[id]++
	}
	for _, id := range order {
		if counts[id] > 1 {
			dErr := &DuplicateIDError{ID: id, Count: counts[id]}
			return contractFailed(dErr), headers, dErr
		}
	}
	return ContractState{OK: true}, headers, nil
}

// NOTE: a deleted_at header is the soft-delete opt-in, not a reserved name; only a duplicate is an error, because it makes the column unaddressable.
func deletedAtColumnOf(headers []string, tab string) (int, error) {
	want := squeezeHeader(deletedAtColumn)
	var found []int
	for i, h := range headers {
		if squeezeHeader(foldHeader(h)) == want {
			found = append(found, i)
		}
	}
	if len(found) > 1 {
		return -1, &DuplicateColumnError{Tab: tab, Column: deletedAtColumn, Columns: found}
	}
	if len(found) == 0 {
		return -1, nil
	}
	return found[0], nil
}

func squeezeHeader(h string) string {
	return strings.NewReplacer(" ", "", "_", "", "-", "").Replace(h)
}

func blankRow(row []string) bool {
	for _, cell := range row {
		if cell != "" {
			return false
		}
	}
	return true
}

func cellAt(row []string, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	return row[col]
}

func contractFailed(err error) ContractState {
	return ContractState{OK: false, Reason: err.Error()}
}
