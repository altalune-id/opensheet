package sheet

import (
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
