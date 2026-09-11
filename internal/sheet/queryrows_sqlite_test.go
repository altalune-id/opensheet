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
	f := newSQLiteFixture(t)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	h := newReadHarness(t, harnessOpts{rowStore: newSQLiteRowStore(t, f)})
	h.google.setRows(http.StatusOK, walkBody)
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
