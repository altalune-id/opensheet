package sheet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
)

func liveRows() []ProjectedRow {
	return []ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
	}
}

func TestRowsDigest_IsStableForIdenticalRows(t *testing.T) {
	t.Parallel()
	first, err := RowsDigest(liveRows())
	require.NoError(t, err)
	second, err := RowsDigest(liveRows())
	require.NoError(t, err)
	require.Equal(t, first, second, "a refetch of identical rows must not move the digest")
}

// TestRowsDigest_CoversTheTombstone is the assertion the conditional bump rests on: a tombstone changes no row_id, no row_index and no cell, so a digest over data alone is byte-identical across a delete.
func TestRowsDigest_CoversTheTombstone(t *testing.T) {
	t.Parallel()
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	live := liveRows()
	tombstoned := liveRows()
	tombstoned[1].DeletedAt = &deletedAt

	liveDigest, err := RowsDigest(live)
	require.NoError(t, err)
	tombstonedDigest, err := RowsDigest(tombstoned)
	require.NoError(t, err)
	require.NotEqual(t, liveDigest, tombstonedDigest)
}

// TestRowsDigest_IgnoresTheTombstoneTimestamp pins why the flag is hashed and not the time: tombstoneAt stands an unparseable marker in with the fetch time, which would churn the digest on every refresh.
func TestRowsDigest_IgnoresTheTombstoneTimestamp(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	late := time.Date(2026, 9, 12, 7, 8, 9, 0, time.UTC)
	first := liveRows()
	first[1].DeletedAt = &early
	second := liveRows()
	second[1].DeletedAt = &late

	firstDigest, err := RowsDigest(first)
	require.NoError(t, err)
	secondDigest, err := RowsDigest(second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestRowsDigest_CoversTheCells(t *testing.T) {
	t.Parallel()
	edited := liveRows()
	edited[1].Data = gsheet.Row{"id": "b", "name": "Bob"}

	before, err := RowsDigest(liveRows())
	require.NoError(t, err)
	after, err := RowsDigest(edited)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestRowsDigest_CoversTheRowIndex(t *testing.T) {
	t.Parallel()
	moved := liveRows()
	moved[1].RowIndex = 3

	before, err := RowsDigest(liveRows())
	require.NoError(t, err)
	after, err := RowsDigest(moved)
	require.NoError(t, err)
	require.NotEqual(t, before, after, "a blank row inserted above a row moves its row_index and nothing else")
}

func TestRowsDigest_FieldsCannotRunTogether(t *testing.T) {
	t.Parallel()
	split := []ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{}},
		{RowID: "bc", RowIndex: 1, Data: gsheet.Row{}},
	}
	shifted := []ProjectedRow{
		{RowID: "ab", RowIndex: 0, Data: gsheet.Row{}},
		{RowID: "c", RowIndex: 1, Data: gsheet.Row{}},
	}
	splitDigest, err := RowsDigest(split)
	require.NoError(t, err)
	shiftedDigest, err := RowsDigest(shifted)
	require.NoError(t, err)
	require.NotEqual(t, splitDigest, shiftedDigest)
}

// TestRowsDigest_EmptyProjectionIsNotTheColumnDefault keeps an empty projection off the "never computed" sentinel the column defaults to.
func TestRowsDigest_EmptyProjectionIsNotTheColumnDefault(t *testing.T) {
	t.Parallel()
	empty, err := RowsDigest(nil)
	require.NoError(t, err)
	require.NotEmpty(t, empty)

	sameEmpty, err := RowsDigest([]ProjectedRow{})
	require.NoError(t, err)
	require.Equal(t, empty, sameEmpty)

	populated, err := RowsDigest(liveRows())
	require.NoError(t, err)
	require.NotEqual(t, empty, populated)
}

func TestRowsDigest_MatchesWhatProjectRowsWrote(t *testing.T) {
	t.Parallel()
	tbl := gsheet.Table{
		Headers: []string{"id", "name", "deleted_at"},
		Rows:    [][]string{{"a", "ada", ""}, {}, {"b", "bob", "2026-09-11"}},
	}
	projected, err := projectRows(tbl, "Sheet1", time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, projected, 2)

	first, err := RowsDigest(projected)
	require.NoError(t, err)
	again, err := projectRows(tbl, "Sheet1", time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	second, err := RowsDigest(again)
	require.NoError(t, err)
	require.Equal(t, first, second, "the same fetched table must project to the same digest whenever it is fetched")
}
