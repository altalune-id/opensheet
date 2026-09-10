package sheet_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type fixHarness struct {
	google *fakeWriteSheets
	reauth *fakes.SheetReauthers
	unex   *int
	wf     *sheet.FixWorkflow
}

func newFixHarness(t *testing.T) *fixHarness {
	t.Helper()
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	h := &fixHarness{
		google: newFakeWriteSheets(t),
		reauth: fakes.NewSheetReauthers(),
		unex:   &calls,
	}
	h.wf = sheet.NewFixWorkflow(
		anySheetSource{}, fakes.NewSheetTokenSources(), h.reauth, h.google.factory(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected,
	)
	return h
}

// writtenColumn decodes the one PUT the fix is allowed to make into its A1 span and its column of values.
func (h *fixHarness) writtenColumn(t *testing.T) (span string, values []string) {
	t.Helper()
	puts := 0
	var put recordedRequest
	for _, r := range h.google.recorded() {
		if r.method == http.MethodPut {
			puts++
			put = r
		}
	}
	require.Equal(t, 1, puts, "the fix must write the whole column in one UpdateRange call, not one per row")

	// SECURITY: RAW is the formula-injection control — a backfill under USER_ENTERED would let a crafted existing cell evaluate on write.
	assert.Contains(t, put.url, "valueInputOption=RAW", "the backfill must be a RAW write")

	parsed, err := url.Parse(put.url)
	require.NoError(t, err)
	_, span, ok := strings.Cut(parsed.Path, "!")
	require.True(t, ok, "the written range %q carries no tab-qualified span", parsed.Path)

	var payload struct {
		Values [][]any `json:"values"`
	}
	require.NoError(t, json.Unmarshal([]byte(put.body), &payload))
	values = make([]string, 0, len(payload.Values))
	for _, row := range payload.Values {
		require.Len(t, row, 1, "the fix writes one cell per row")
		text, _ := row[0].(string)
		values = append(values, text)
	}
	return span, values
}

func TestFixWorkflow_AddIDColumn_WritesTheHeaderWhenAbsent(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["name","qty"],["ada","3"],["bo","5"]]}`)

	out, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.NoError(t, err)
	assert.True(t, out.HeaderWritten)
	assert.Equal(t, "C", out.Column, "the header goes in the first column after the last one")
	assert.Equal(t, 2, out.RowsFilled)

	span, values := h.writtenColumn(t)
	assert.Equal(t, "C1:C3", span)
	require.Len(t, values, 3)
	assert.Equal(t, "id", values[0])
	for _, id := range values[1:] {
		assert.Len(t, id, 21, "a generated id is a nanoid.New(21)")
	}
	assert.NotEqual(t, values[1], values[2], "every backfilled id must be unique")
	assert.Equal(t, 0, *h.unex, "a fix that succeeded must report no incident")
}

func TestFixWorkflow_AddIDColumn_ReusesAnExistingIDColumn(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["name","id","qty"],["ada","","3"],["bo","keep-me","5"],["cyd","","7"]]}`)

	out, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.NoError(t, err)
	assert.False(t, out.HeaderWritten, "an existing id header must not be rewritten")
	assert.Equal(t, "B", out.Column)
	assert.Equal(t, 2, out.RowsFilled)

	span, values := h.writtenColumn(t)
	assert.Equal(t, "B2:B4", span, "the span starts below the header row when the header already exists")
	require.Len(t, values, 3)
	assert.Len(t, values[0], 21)
	assert.Equal(t, "keep-me", values[1], "an id that already exists must survive the backfill")
	assert.Len(t, values[2], 21)
}

func TestFixWorkflow_AddIDColumn_SkipsBlankRows(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["name"],["ada"],[],["cyd"]]}`)

	out, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.NoError(t, err)
	assert.Equal(t, 2, out.RowsFilled, "a blank row needs no id")

	span, values := h.writtenColumn(t)
	assert.Equal(t, "B1:B4", span)
	require.Len(t, values, 4)
	assert.Equal(t, "id", values[0])
	assert.Len(t, values[1], 21)
	assert.Empty(t, values[2], "writing an id into a blank row would make it look real")
	assert.Len(t, values[3], 21)
}

func TestFixWorkflow_AddIDColumn_RefusesWhenTheTargetColumnHoldsData(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	// The header row names two columns, but a data row carries a third value — the column an id header would claim.
	h.google.setTable(http.StatusOK, `{"values":[["name","qty"],["ada","3"],["bo","5","stray"]]}`)

	_, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.Error(t, err)
	require.True(t, sheet.IsColumnNotEmptyError(err), "error = %v (%T), want ColumnNotEmptyError", err, err)

	app, ok := apperror.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperror.CodeSheetColumnNotEmpty, app.Code())
	assert.Equal(t, http.StatusConflict, app.HTTPStatus())

	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPut, r.method, "a refused fix must never write to the spreadsheet — there is no undo")
	}
}

func TestFixWorkflow_AddIDColumn_RefusesWhenThereIsNothingToFix(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, idNameTable)

	_, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.Error(t, err)
	require.True(t, sheet.IsNothingToFixError(err), "error = %v (%T), want NothingToFixError", err, err)

	app, ok := apperror.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperror.CodeSheetNothingToFix, app.Code())
	assert.Equal(t, http.StatusConflict, app.HTTPStatus())

	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPut, r.method, "a tab that already satisfies the contract must not be written to")
	}
}

func TestFixWorkflow_AddIDColumn_RefusesTwoIDColumns(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["id","ID"],["a","b"]]}`)

	_, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.True(t, sheet.IsAmbiguousIDColumnError(err), "error = %v (%T), want AmbiguousIDColumnError", err, err)
	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPut, r.method)
	}
}

// SECURITY: write capability is discovered from Google's refusal, never pre-checked — credentials carries no granted-scopes column.
func TestFixWorkflow_AddIDColumn_SurfacesAReadOnlyRefusal(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["name"],["ada"]]}`)
	h.google.setUpdate(http.StatusForbidden, `{"error":{"code":403,"message":"insufficient authentication scopes"}}`)

	_, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.Error(t, err)
	require.True(t, gworkspace.IsPermissionDeniedError(err), "error = %v (%T), want PermissionDeniedError so the form can name the manual remedy", err, err)

	app, ok := apperror.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperror.CodeGooglePermissionDenied, app.Code())
	assert.Equal(t, 0, *h.unex, "a read-only credential is an expected outcome, not an incident")
}

func TestFixWorkflow_AddIDColumn_ResolvesABlankTabToTheFirstOne(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["name"],["ada"]]}`)

	out, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "  ")
	require.NoError(t, err)
	assert.Equal(t, "First", out.Tab)

	span, _ := h.writtenColumn(t)
	assert.Equal(t, "B1:B2", span)
}

// The fix returns to the form; publishing stays a separate, deliberate step.
func TestFixWorkflow_AddIDColumn_PublishesNothing(t *testing.T) {
	t.Parallel()
	h := newFixHarness(t)
	h.google.setTable(http.StatusOK, `{"values":[["name"],["ada"]]}`)

	_, err := h.wf.AddIDColumn(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	require.NoError(t, err)

	gets, puts, others := 0, 0, 0
	for _, r := range h.google.recorded() {
		switch r.method {
		case http.MethodGet:
			gets++
		case http.MethodPut:
			puts++
		default:
			others++
		}
	}
	assert.Equal(t, 1, gets, "one read of the tab")
	assert.Equal(t, 1, puts, "one write of the id column")
	assert.Equal(t, 0, others, "the fix appends nothing and creates nothing")
}
