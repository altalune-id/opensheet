package sheet

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
)

func TestRowOf_StripsDeletedAt(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"deleted_at", "Deleted At", "deleted-at", "DELETED_AT", "deletedAt", " deleted_at "} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()
			got := rowOf([]string{"id", "name", spelling}, []any{"a", "ada", ""})
			require.Equal(t, gsheet.Row{"id": "a", "name": "ada"}, got)
		})
	}
}

func TestRowOf_KeysARowLikeTheProjection(t *testing.T) {
	t.Parallel()
	tbl := gsheet.Table{
		Headers: []string{"id", "name", "Deleted At"},
		Rows:    [][]string{{"a", "ada", ""}},
	}
	projected, err := projectRows(tbl, "Sheet1", time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, projected, 1)

	idCol, err := idColumnOf(tbl.Headers, "Sheet1")
	require.NoError(t, err)
	cells, err := mergeRow(tbl.Headers, tbl.Rows[0], idCol, map[string]any{"name": "Ada"}, "Sheet1")
	require.NoError(t, err)
	patched := rowOf(tbl.Headers, cells)

	require.Equal(t,
		slices.Sorted(maps.Keys(projected[0].Data)),
		slices.Sorted(maps.Keys(patched)),
		"a PATCH response and a projected row must key one row identically")
	require.Equal(t, gsheet.Row{"id": "a", "name": "Ada"}, patched)
}

// The rebuild must be a Go json.Marshal, never a SQL aggregation: jsonb normalizes \u0026 back to &
// and reorders object keys, so a SQL-side rebuild would change the ETag for rows nobody edited.
func TestRebuildPayload_IsByteIdenticalToJSONMarshal(t *testing.T) {
	t.Parallel()
	deletedAt := time.Now().UTC()
	live := []gsheet.Row{
		{"id": "a", "name": "Bed & Breakfast", "note": "a < b > c"},
		{"id": "b", "zeta": "1", "alpha": "2"},
	}
	rows := []ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: live[0]},
		{RowID: "gone", RowIndex: 1, Data: gsheet.Row{"id": "gone"}, DeletedAt: &deletedAt},
		{RowID: "b", RowIndex: 2, Data: live[1]},
	}

	got, err := rebuildPayload(rows)
	require.NoError(t, err)
	want, err := json.Marshal(live)
	require.NoError(t, err)

	require.Equal(t, string(want), string(got))
	require.Contains(t, string(got), `\u0026`, "encoding/json escapes &, and jsonb does not")
	require.Contains(t, string(got), `\u003c`, "encoding/json escapes <, and jsonb does not")
	require.Equal(t, etagOf(want), etagOf(got), "the rebuilt ETag must be comparable across a refresh and a rebuild")
}

func TestRebuildPayload_MarshalsAnEmptyProjectionAsAnEmptyArray(t *testing.T) {
	t.Parallel()
	got, err := rebuildPayload(nil)
	require.NoError(t, err)
	require.Equal(t, "[]", string(got), "an empty tab must not serialize as null")
}
