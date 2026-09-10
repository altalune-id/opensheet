package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/spreadsheet"
)

const noIDColumnTab = `{"values":[["name","qty"],["ada","3"],[],["cyd","7"]]}`

func (f *sheetsFixture) publishNoIDColumn(t *testing.T, sp *spreadsheet.Spreadsheet) string {
	t.Helper()
	f.Google.setRows(noIDColumnTab)
	form := url.Values{
		"spreadsheet_id": {sp.ID.String()},
		"slug":           {"rates"},
		"tab":            {"Q1"},
		"visibility":     {"key"},
	}
	rec := f.do(t, http.MethodPost, f.path("/sheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}

func TestSheetHandler_PublishWithoutAnIDColumnOffersTheFix(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)

	body := f.publishNoIDColumn(t, sp)
	assert.Contains(t, body, `data-error="sheets"`)
	assert.Contains(t, body, "data-fix-id-column", "the no-id error must carry the offer to add the column")
	assert.Contains(t, body, "/spreadsheets/"+sp.ID.String()+"/fix-id-column?tab=Q1")
	// The fixture carries no translator, so the button renders its key — the copy itself is asserted in TestFixKeys_ResolveInEveryLocale.
	assert.Contains(t, body, "sheets.fix_id_column")

	items, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items, "a refused publish must create nothing")
	assert.Empty(t, f.Google.writesReceived(), "publishing must never write to the spreadsheet")
}

func TestSheetHandler_FixIDColumnWritesTheColumnAndPublishesNothing(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	f.publishNoIDColumn(t, sp)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/fix-id-column?tab=Q1"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `data-notice="sheets"`)
	assert.Contains(t, body, "wrote an id column")
	assert.NotContains(t, body, `data-error="sheets"`)

	writes := f.Google.writesReceived()
	require.Len(t, writes, 1, "the whole column is written in one request, not one per row")
	assert.Contains(t, writes[0].url, "valueInputOption=RAW")
	assert.Contains(t, writes[0].url, "C1%3AC4", "the id header claims the first free column")

	var payload struct {
		Values [][]string `json:"values"`
	}
	require.NoError(t, json.Unmarshal([]byte(writes[0].body), &payload))
	require.Len(t, payload.Values, 4)
	assert.Equal(t, "id", payload.Values[0][0])
	assert.Len(t, payload.Values[1][0], 21)
	assert.Empty(t, payload.Values[2][0], "the blank row keeps its blank id")
	assert.Len(t, payload.Values[3][0], 21)

	items, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items, "the fix returns to the form; publishing stays a separate step")
}

// SECURITY: write capability is discovered from Google's refusal, never pre-checked.
func TestSheetHandler_FixIDColumnSurfacesTheManualRemedyOnARefusal(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	f.publishNoIDColumn(t, sp)
	f.Google.refuseWrites()

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/fix-id-column?tab=Q1"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `data-error="sheets"`)
	for _, want := range []string{"refused", "headed id", "write scope"} {
		assert.True(t, strings.Contains(body, want), "the refusal must name the manual remedy; missing %q", want)
	}
	assert.NotContains(t, body, `data-notice="sheets"`)
}

func TestSheetHandler_FixIDColumnRefusesWhenThereIsNothingToFix(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/fix-id-column?tab=Q1"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "already has an id column")
	assert.Empty(t, f.Google.writesReceived(), "a tab that needs no fix must not be written to")
}

func TestSheetHandler_FixIDColumnRejectsAnUnregisteredSpreadsheet(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/00000000-0000-7000-8000-000000000000/fix-id-column?tab=Q1"), "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, f.Google.writesReceived())
}
