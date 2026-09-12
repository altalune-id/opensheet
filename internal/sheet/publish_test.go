package sheet_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type anySheetSource struct{}

func (anySheetSource) SourceFor(_ context.Context, spreadsheetID uuid.UUID) (sheet.Source, error) {
	return sheet.Source{GoogleFileID: "FILE", CredentialID: spreadsheetID}, nil
}

type publishHarness struct {
	google *fakeSheets
	reauth *fakes.SheetReauthers
	unex   *int
	wf     *sheet.PublishWorkflow
}

func newPublishHarness(t *testing.T) *publishHarness {
	t.Helper()
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	h := &publishHarness{
		google: newFakeSheets(t),
		reauth: fakes.NewSheetReauthers(),
		unex:   &calls,
	}
	h.wf = sheet.NewPublishWorkflow(
		anySheetSource{}, fakes.NewSheetTokenSources(), h.reauth, h.google.factory(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected,
	)
	return h
}

func valuesBody(t *testing.T, headers []string, rows [][]string) string {
	t.Helper()
	values := make([][]string, 0, len(rows)+1)
	values = append(values, headers)
	values = append(values, rows...)
	raw, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	return string(raw)
}

// The tab has the column and no deletions — the case an inferred flag reported as false, so a client
// concluded delete was unsupported and never tried.
func TestPublishWorkflow_Validate_ReadsTheSoftDeleteOptInFromTheHeaderRow(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		headers []string
		want    bool
	}{
		"no deleted_at column": {[]string{"id", "name"}, false},
		"deleted_at":           {[]string{"id", "deleted_at"}, true},
		"Deleted At":           {[]string{"id", "Deleted At"}, true},
		"deletedAt":            {[]string{"id", "deletedAt"}, true},
		"a lookalike column":   {[]string{"id", "deleted"}, false},
		"deleted_at is not id": {[]string{"deleted_at", "id"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newPublishHarness(t)
			row := make([]string, len(tt.headers))
			row[slices.Index(tt.headers, "id")] = "a"
			h.google.setRows(http.StatusOK, valuesBody(t, tt.headers, [][]string{row}))

			state, _, err := h.wf.Validate(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
			if err != nil {
				t.Fatalf("Validate err = %v", err)
			}
			if state.SoftDelete != tt.want {
				t.Errorf("SoftDelete = %v, want %v for headers %q", state.SoftDelete, tt.want, tt.headers)
			}
		})
	}
}

func TestPublishWorkflow_Validate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		headers []string
		rows    [][]string
		wantErr func(error) bool
	}{
		{"happy", []string{"id", "name"}, [][]string{{"a", "ada"}}, nil},
		{"no id column", []string{"name"}, [][]string{{"ada"}}, sheet.IsNoIDColumnError},
		{"two id columns", []string{"id", "ID"}, [][]string{{"a", "b"}}, sheet.IsAmbiguousIDColumnError},
		{"two deleted_at columns", []string{"id", "deleted_at", "Deleted At"}, [][]string{{"a", "", ""}}, sheet.IsDuplicateColumnError},
		{"deleted_at once is fine", []string{"id", "deleted_at"}, [][]string{{"a", ""}}, nil},
		{"duplicate id", []string{"id"}, [][]string{{"a"}, {"a"}}, sheet.IsDuplicateIDError},
		{"blank row skipped", []string{"id"}, [][]string{{"a"}, {}, {"c"}}, nil},
		{"content but empty id", []string{"id", "name"}, [][]string{{"", "ada"}}, sheet.IsEmptyIDError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newPublishHarness(t)
			h.google.setRows(http.StatusOK, valuesBody(t, tt.headers, tt.rows))

			state, headers, err := h.wf.Validate(t.Context(), uuid.Must(uuid.NewV7()), "Q1")

			if !slices.Equal(headers, tt.headers) {
				t.Errorf("headers = %#v, want %#v", headers, tt.headers)
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate err = %v, want nil", err)
				}
				if !state.OK || state.Reason != "" {
					t.Errorf("state = %+v, want a satisfied contract", state)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate err = nil, want a contract error")
			}
			if !tt.wantErr(err) {
				t.Errorf("Validate err = %v (%T), want the documented contract error", err, err)
			}
			if state.OK {
				t.Error("state.OK = true, want false alongside a contract error")
			}
			if state.Reason == "" {
				t.Error("state.Reason is empty, want the reason capabilities reports")
			}
			if *h.unex != 0 {
				t.Errorf("unexpected() calls = %d, want 0 for an expected contract failure", *h.unex)
			}
		})
	}
}

func TestPublishWorkflow_Validate_ResolvesTheFirstTabWhenTabIsEmpty(t *testing.T) {
	t.Parallel()
	h := newPublishHarness(t)
	h.google.setRows(http.StatusOK, valuesBody(t, []string{"id"}, [][]string{{"a"}}))

	state, _, err := h.wf.Validate(t.Context(), uuid.Must(uuid.NewV7()), "")
	if err != nil {
		t.Fatalf("Validate err = %v", err)
	}
	if !state.OK {
		t.Errorf("state = %+v, want a satisfied contract", state)
	}
	if _, meta := h.google.counts(); meta == 0 {
		t.Error("metaCalls = 0, want the first tab resolved through Google")
	}
}

func TestPublishWorkflow_Validate_FailsClosedOnAGoogleError(t *testing.T) {
	t.Parallel()
	h := newPublishHarness(t)
	h.google.setRows(http.StatusServiceUnavailable, `{"error":{"code":503,"message":"backend error"}}`)

	state, _, err := h.wf.Validate(t.Context(), uuid.Must(uuid.NewV7()), "Q1")
	if err == nil {
		t.Fatal("Validate err = nil, want the Google failure returned")
	}
	if state.OK {
		t.Error("state.OK = true, want a Google failure to report nothing about the contract")
	}
}

func TestPublishWorkflow_Validate_MarksReauthWhenGoogleRejectsTheCredential(t *testing.T) {
	t.Parallel()
	h := newPublishHarness(t)
	h.google.setRows(http.StatusUnauthorized, `{"error":{"code":401,"message":"Invalid Credentials"}}`)

	spreadsheetID := uuid.Must(uuid.NewV7())
	if _, _, err := h.wf.Validate(t.Context(), spreadsheetID, "Q1"); err == nil {
		t.Fatal("Validate err = nil, want the credential failure returned")
	}
	if marked := h.reauth.MarkedIDs(); len(marked) != 1 || marked[0] != spreadsheetID {
		t.Errorf("marked credentials = %v, want the tab's credential flagged once", marked)
	}
}
