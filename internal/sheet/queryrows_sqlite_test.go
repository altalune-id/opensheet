package sheet_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/sheet"
)

// NOTE: an interior blank row so row_index carries a gap, and a tombstone so a live row is skipped — the two shapes a keyset walk gets wrong.
const walkBody = `{"values":[["id","name","deleted_at"],["a","apple",""],[],` +
	`["b","pear",""],["c","plum","2026-01-02"],["d","peach",""]]}`

type queryFixture struct {
	h  *readHarness
	f  *fixture
	sh *sheet.Sheet
}

func newQueryFixture(t *testing.T) *queryFixture {
	t.Helper()
	return newQueryFixtureOf(t, walkBody, harnessOpts{})
}

func newQueryFixtureOf(t *testing.T, body string, opts harnessOpts) *queryFixture {
	t.Helper()
	f := newSQLiteFixture(t)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	opts.rowStore = newSQLiteRowStore(t, f)
	h := newReadHarness(t, opts)
	h.google.setRows(http.StatusOK, body)
	h.srcs.Set(sh.SpreadsheetID, sheet.Source{
		GoogleFileID: "gfile-1", CredentialID: uuid.Must(uuid.NewV7()),
	})
	return &queryFixture{h: h, f: f, sh: sh}
}

// resolve re-reads the aggregate the way a fresh request does, so validated_at and the generation are never stale in memory.
func (q *queryFixture) resolve(t *testing.T) {
	t.Helper()
	sh, err := q.f.store.ByID(q.f.ctx(), q.sh.ID)
	require.NoError(t, err)
	q.sh = sh
}

func (q *queryFixture) expire(t *testing.T) {
	t.Helper()
	q.resolve(t)
	past := time.Now().UTC().Add(-time.Hour)
	q.sh.ValidatedAt = &past
}

func (q *queryFixture) page(t *testing.T, f sheet.RowFilter) sheet.FilteredRows {
	t.Helper()
	got, err := q.h.wf.QueryRows(q.f.ctx(), q.sh, f)
	require.NoError(t, err)
	return got
}

func namesOf(t *testing.T, payload []byte) []string {
	t.Helper()
	var rows []gsheet.Row
	require.NoError(t, json.Unmarshal(payload, &rows))
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row["name"])
	}
	return out
}

func TestQueryRows_KeysetWalkVisitsEveryLiveRowExactlyOnce(t *testing.T) {
	q := newQueryFixture(t)

	var (
		seen   []string
		cursor string
	)
	for range 5 {
		got := q.page(t, sheet.RowFilter{Limit: "1", Cursor: cursor})
		seen = append(seen, namesOf(t, got.Payload)...)
		cursor = got.NextCursor
		if cursor == "" {
			break
		}
		q.resolve(t)
	}
	assert.Equal(t, []string{"apple", "pear", "peach"}, seen,
		"a walk at limit=1 must visit every live row once, in row_index order, across a blank row and a tombstone")

	whole := q.page(t, sheet.RowFilter{Limit: "10"})
	assert.Equal(t, seen, namesOf(t, whole.Payload), "the walk must equal the unpaged read")
}

func TestQueryRows_FiltersOnTheFirstReadOfANeverRefreshedSheet(t *testing.T) {
	q := newQueryFixture(t)

	got := q.page(t, sheet.RowFilter{Where: []string{"name:contains:PEA"}})

	assert.Equal(t, []string{"pear", "peach"}, namesOf(t, got.Payload),
		"the clauses must be validated and applied against the columns the same request projected")
	assert.Empty(t, got.NextCursor, "a complete page advertises no next one")
}

func TestQueryRows_TheLastPageLinksNothing(t *testing.T) {
	q := newQueryFixture(t)

	full := q.page(t, sheet.RowFilter{Limit: "3"})
	assert.Len(t, namesOf(t, full.Payload), 3)
	assert.Empty(t, full.NextCursor, "a page holding every remaining row must not advertise another")

	q.resolve(t)
	short := q.page(t, sheet.RowFilter{Limit: "2"})
	assert.NotEmpty(t, short.NextCursor, "a trimmed page must advertise its successor")
}

func TestPagination_SurvivesARefreshThatChangedNothing(t *testing.T) {
	q := newQueryFixture(t)

	first := q.page(t, sheet.RowFilter{Limit: "1"})
	require.Equal(t, []string{"apple"}, namesOf(t, first.Payload))
	require.NotEmpty(t, first.NextCursor)
	state, err := newSQLiteRowStore(t, q.f).StateOf(q.f.ctx(), q.sh.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Generation)

	// NOTE: a TTL refresh returning byte-identical rows; on the old unconditional bump this refused the cursor.
	q.expire(t)
	second := q.page(t, sheet.RowFilter{Limit: "1", Cursor: first.NextCursor})
	assert.Equal(t, []string{"pear"}, namesOf(t, second.Payload))
	after, err := newSQLiteRowStore(t, q.f).StateOf(q.f.ctx(), q.sh.ID)
	require.NoError(t, err)
	assert.Equal(t, state.Generation, after.Generation, "a refresh that changed nothing moved the generation")
	assert.Equal(t, state.Digest, after.Digest)

	q.h.google.setRows(http.StatusOK, `{"values":[["id","name","deleted_at"],["a","apricot",""],[],`+
		`["b","pear",""],["c","plum","2026-01-02"],["d","peach",""]]}`)
	q.expire(t)
	_, err = q.h.wf.QueryRows(q.f.ctx(), q.sh, sheet.RowFilter{Limit: "1", Cursor: first.NextCursor})
	assert.True(t, sheet.IsStaleCursorError(err),
		"err = %v, want a cursor issued before an edit refused rather than silently resumed", err)
}

// NOTE: a row write clears content_digest to ”, which no cursor can carry, so the gate must refresh on it even inside the TTL or the next page fails on an invariant breach.
func TestQueryRows_AClearedDigestIsRecomputedBeforeACursorIsIssued(t *testing.T) {
	q := newQueryFixture(t)

	first := q.page(t, sheet.RowFilter{Limit: "1"})
	require.NotEmpty(t, first.NextCursor)
	before, _ := q.h.google.counts()

	store := newSQLiteRowStore(t, q.f)
	require.NoError(t, store.UpsertRow(q.f.ctx(), sheet.SnapshotKey{SheetID: q.sh.ID, Tab: "Rates"},
		sheet.ProjectedRow{RowID: "e", RowIndex: 5, Data: gsheet.Row{"id": "e", "name": "quince"}}))
	cleared, err := store.StateOf(q.f.ctx(), q.sh.ID)
	require.NoError(t, err)
	require.Empty(t, cleared.Digest, "the write did not clear the digest, so this proves nothing")

	q.resolve(t)
	again := q.page(t, sheet.RowFilter{Limit: "1"})
	assert.NotEmpty(t, again.NextCursor, "no cursor was issued, so the cleared digest was never recomputed")
	after, _ := q.h.google.counts()
	assert.Equal(t, before+1, after, "a cleared digest must refresh even inside the TTL")
}

// NOTE: a qty column whose text order and numeric order disagree, so a hint that never reaches the driver is visible in the answer.
const typedWalkBody = `{"values":[["id","qty","deleted_at"],["a","9",""],["b","9.5",""],` +
	`["c","10",""],["d","100",""],["e","abc",""]]}`

func idsOf(t *testing.T, payload []byte) []string {
	t.Helper()
	var rows []gsheet.Row
	require.NoError(t, json.Unmarshal(payload, &rows))
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row["id"])
	}
	return out
}

// walkNames pages a sorted read to exhaustion through the route, re-resolving the aggregate between pages as a fresh request does.
func (q *queryFixture) walkNames(t *testing.T, f sheet.RowFilter) ([]string, []int) {
	t.Helper()
	var (
		seen     []string
		versions []int
	)
	for range 8 {
		got := q.page(t, f)
		seen = append(seen, namesOf(t, got.Payload)...)
		if got.NextCursor == "" {
			return seen, versions
		}
		decoded, err := sheet.DecodeRowCursor(got.NextCursor)
		require.NoError(t, err)
		versions = append(versions, decoded.Version)
		f.Cursor = got.NextCursor
		q.resolve(t)
	}
	t.Fatal("the sorted walk did not terminate")
	return nil, nil
}

// TestQueryRows_ASortedReadOrdersEveryPage is the route-level half of task 4's walk: the order, the pages and the v2 envelope all reach a caller only through this path.
func TestQueryRows_ASortedReadOrdersEveryPage(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want []string
	}{
		{"name:asc", []string{"apple", "peach", "pear"}},
		{"name:desc", []string{"pear", "peach", "apple"}},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			q := newQueryFixture(t)

			whole := q.page(t, sheet.RowFilter{Sort: []string{tc.spec}})
			assert.Equal(t, tc.want, namesOf(t, whole.Payload),
				"an unpaged sorted read must order every live row")

			q.resolve(t)
			walked, versions := q.walkNames(t, sheet.RowFilter{Limit: "1", Sort: []string{tc.spec}})
			assert.Equal(t, tc.want, walked, "the paged walk must equal the unpaged read")
			assert.Equal(t, []int{2, 2}, versions, "a sorted page must issue a v2 cursor")
		})
	}
}

// TestQueryRows_ASortColumnIsResolvedToItsStoredSpelling exists because both drivers use RowSort.Column verbatim as the JSON field name: left as the client typed it, the cell reads NULL for every row and the answer silently falls back to row_index order.
func TestQueryRows_ASortColumnIsResolvedToItsStoredSpelling(t *testing.T) {
	q := newQueryFixture(t)

	got := q.page(t, sheet.RowFilter{Sort: []string{"NAME:desc"}})

	assert.Equal(t, []string{"pear", "peach", "apple"}, namesOf(t, got.Payload),
		"the sort column must reach the store in the spelling the projection holds")
}

// NOTE: the route's own assertion that the hint survives to the driver — the text order of these cells disagrees with their numeric order in both directions.
func TestQueryRows_ATypedClauseComparesNumericallyThroughTheRoute(t *testing.T) {
	for _, tc := range []struct {
		where string
		want  []string
	}{
		{"qty:num.gt:10", []string{"d"}},
		{"qty:gt:10", []string{"a", "b", "d", "e"}},
		{"qty:num.lte:10", []string{"a", "b", "c"}},
		{"qty:num.eq:10", []string{"c"}},
	} {
		t.Run(tc.where, func(t *testing.T) {
			q := newQueryFixtureOf(t, typedWalkBody, harnessOpts{})

			got := q.page(t, sheet.RowFilter{Where: []string{tc.where}})

			assert.Equal(t, tc.want, idsOf(t, got.Payload))
		})
	}
}

// NOTE: nulls last in both directions, and a cell outside the grammar is a null — the row holding "abc" must not lead a descending page.
func TestQueryRows_ASortedReadOrdersNonNumericCellsLast(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want []string
	}{
		{"qty:num.asc", []string{"a", "b", "c", "d", "e"}},
		{"qty:num.desc", []string{"d", "c", "b", "a", "e"}},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			q := newQueryFixtureOf(t, typedWalkBody, harnessOpts{})

			got := q.page(t, sheet.RowFilter{Sort: []string{tc.spec}})

			assert.Equal(t, tc.want, idsOf(t, got.Payload))
		})
	}
}

// TestQueryRows_TheSortedWalkStopsAtTheConfiguredDepth walks the real cursor the route issued, so the cap is tested against the page count the encoder wrote rather than one a test hand-set.
func TestQueryRows_TheSortedWalkStopsAtTheConfiguredDepth(t *testing.T) {
	q := newQueryFixtureOf(t, walkBody, harnessOpts{maxSortPages: 2})

	first := q.page(t, sheet.RowFilter{Limit: "1", Sort: []string{"name:asc"}})
	require.Equal(t, []string{"apple"}, namesOf(t, first.Payload))
	require.NotEmpty(t, first.NextCursor)

	q.resolve(t)
	second := q.page(t, sheet.RowFilter{Limit: "1", Sort: []string{"name:asc"}, Cursor: first.NextCursor})
	assert.Equal(t, []string{"peach"}, namesOf(t, second.Payload), "the cap must serve exactly maxSortPages pages")
	require.NotEmpty(t, second.NextCursor, "the third page exists; the cap is what refuses it")

	q.resolve(t)
	_, err := q.h.wf.QueryRows(q.f.ctx(), q.sh,
		sheet.RowFilter{Limit: "1", Sort: []string{"name:asc"}, Cursor: second.NextCursor})
	assert.True(t, sheet.IsSortDepthError(err), "err = %v, want SHT040 one page past the cap", err)
}
