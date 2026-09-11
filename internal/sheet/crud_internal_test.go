package sheet

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
)

func ptr(v string) *string { return &v }

// TestNextRowCursor_DerivesTheNullRankAndTheSortValue is the third and final place the grammar is applied: a wrong null rank under a text sort drops the row after the first null, and a blank cell is not a null one.
func TestNextRowCursor_DerivesTheNullRankAndTheSortValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		sort  *RowSort
		data  gsheet.Row
		pages int
		want  RowCursor
	}{
		{
			name:  "unsorted carries no sort fields at all",
			sort:  nil,
			data:  gsheet.Row{"qty": "5"},
			pages: 3,
			want:  RowCursor{Digest: "d", RowIndex: 7},
		},
		{
			name:  "text value",
			sort:  &RowSort{Column: "name"},
			data:  gsheet.Row{"name": "ada"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, SortValue: ptr("ada"), Page: 1},
		},
		{
			name:  "a blank text cell ranks zero and carries the empty string",
			sort:  &RowSort{Column: "name"},
			data:  gsheet.Row{"name": ""},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, SortValue: ptr(""), Page: 1},
		},
		{
			name:  "a missing key ranks null and carries nothing",
			sort:  &RowSort{Column: "name"},
			data:  gsheet.Row{"other": "x"},
			pages: 2,
			want:  RowCursor{Digest: "d", RowIndex: 7, NullRank: 1, Page: 2},
		},
		{
			name:  "num value rides as its raw cell text",
			sort:  &RowSort{Column: "qty", Hint: RowHintNum},
			data:  gsheet.Row{"qty": "05"},
			pages: 4,
			want:  RowCursor{Digest: "d", RowIndex: 7, SortValue: ptr("05"), Page: 4},
		},
		{
			name:  "a cell outside the num grammar ranks null",
			sort:  &RowSort{Column: "qty", Hint: RowHintNum},
			data:  gsheet.Row{"qty": "abc"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, NullRank: 1, Page: 1},
		},
		{
			name:  "a blank cell ranks null under a num sort",
			sort:  &RowSort{Column: "qty", Hint: RowHintNum},
			data:  gsheet.Row{"qty": ""},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, NullRank: 1, Page: 1},
		},
		{
			name:  "a cell past fifteen significant digits ranks null",
			sort:  &RowSort{Column: "qty", Hint: RowHintNum},
			data:  gsheet.Row{"qty": "123456789012345.101"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, NullRank: 1, Page: 1},
		},
		{
			name:  "date value",
			sort:  &RowSort{Column: "when", Hint: RowHintDate},
			data:  gsheet.Row{"when": "2026-01-02T00:00:00Z"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, SortValue: ptr("2026-01-02T00:00:00Z"), Page: 1},
		},
		{
			name:  "a cell outside the date grammar ranks null",
			sort:  &RowSort{Column: "when", Hint: RowHintDate},
			data:  gsheet.Row{"when": "01/02/2026"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, NullRank: 1, Page: 1},
		},
		{
			name:  "a fractional second is outside the date grammar",
			sort:  &RowSort{Column: "when", Hint: RowHintDate},
			data:  gsheet.Row{"when": "2026-01-02T00:00:00.5Z"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, NullRank: 1, Page: 1},
		},
		{
			name:  "a descending sort derives the same cursor as an ascending one",
			sort:  &RowSort{Column: "name", Desc: true},
			data:  gsheet.Row{"name": "ada"},
			pages: 1,
			want:  RowCursor{Digest: "d", RowIndex: 7, SortValue: ptr("ada"), Page: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := nextRowCursor("d", tc.sort, ProjectedRow{RowIndex: 7, Data: tc.data}, tc.pages)
			assert.Equal(t, tc.want.Digest, got.Digest)
			assert.Equal(t, tc.want.RowIndex, got.RowIndex)
			assert.Equal(t, tc.want.NullRank, got.NullRank, "the null rank is what the keyset specialises on")
			assert.Equal(t, tc.want.Page, got.Page)
			if tc.want.SortValue == nil {
				assert.Nil(t, got.SortValue, "a null cell carries no sort value, never a pointer to the empty string")
				return
			}
			require.NotNil(t, got.SortValue, "a ranked cell must carry its raw text")
			assert.Equal(t, *tc.want.SortValue, *got.SortValue)
		})
	}
}

// TestNextRowCursor_IsAcceptedByTheSortedEncoder pins the derivation to the codec the route uses: NullRank outside {0,1} or a negative Page is a 500, and EncodeRowCursor would drop all three sorted fields.
func TestNextRowCursor_IsAcceptedByTheSortedEncoder(t *testing.T) {
	t.Parallel()
	sort := &RowSort{Column: "name"}
	for _, data := range []gsheet.Row{{"name": "ada"}, {"name": ""}, {"other": "x"}} {
		cursor := nextRowCursor("abc", sort, ProjectedRow{RowIndex: 4, Data: data}, 1)
		raw, err := encodeNextRowCursor(sort, cursor)
		require.NoError(t, err)
		decoded, err := DecodeRowCursor(raw)
		require.NoError(t, err)
		want := cursor
		want.Version = rowSortedCursorVersion
		require.Equal(t, want, decoded, "the cursor must survive the envelope unchanged")
	}
}

// TestEncodeNextRowCursor_UnsortedBytesAreUnchanged keeps 3a's in-flight cursors valid: an unsorted page still encodes v1, whatever the derivation now computes.
func TestEncodeNextRowCursor_UnsortedBytesAreUnchanged(t *testing.T) {
	t.Parallel()
	raw, err := encodeNextRowCursor(nil, nextRowCursor("abc", nil, ProjectedRow{RowIndex: 4}, 9))
	require.NoError(t, err)
	body, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":1,"d":"abc","r":4}`, string(body))
	for _, field := range []string{`"n"`, `"s"`, `"p"`} {
		assert.False(t, strings.Contains(string(body), field), "an unsorted cursor carries no %s", field)
	}
	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &probe))
	assert.Len(t, probe, 3)
}

func TestPagesServed_CountsThePagesAlreadyServed(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, pagesServed(RowWindow{}), "the cursor issued after the first page carries 1")
	assert.Equal(t, 1, pagesServed(RowWindow{Cursor: &RowCursor{Page: 0}}))
	assert.Equal(t, 4, pagesServed(RowWindow{Cursor: &RowCursor{Page: 3}}))
}
