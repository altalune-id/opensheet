package sheet

import (
	"regexp"
	"strings"
	"testing"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

func renderedOrderBy(t *testing.T, query string) string {
	t.Helper()
	_, after, found := strings.Cut(query, "ORDER BY ")
	require.True(t, found, "the statement must carry an ORDER BY: %s", query)
	clause, _, found := strings.Cut(after, "\nLIMIT")
	require.True(t, found, "the statement must carry a LIMIT: %s", query)
	return clause
}

func pgRenderedOrderBy(t *testing.T, sort *RowSort) string {
	t.Helper()
	rows := pgent.NewSheetRows("public", "opensheet_")
	query, _ := postgres.SELECT(rows.RowID).
		FROM(rows).
		WHERE(rows.DeletedAt.IS_NULL()).
		ORDER_BY(pgRowOrder(rows.Data, rows.RowIndex, sort)...).
		LIMIT(1).
		Sql()
	return renderedOrderBy(t, query)
}

func sqliteRenderedOrderBy(t *testing.T, sort *RowSort) string {
	t.Helper()
	rows := sqliteent.NewSheetRows("opensheet_")
	order, err := sqliteRowOrder(rows.Data, rows.RowIndex, sort)
	require.NoError(t, err)
	query, _ := sqlite.SELECT(rows.RowID).
		FROM(rows).
		WHERE(rows.DeletedAt.IS_NULL()).
		ORDER_BY(order...).
		LIMIT(1).
		Sql()
	return renderedOrderBy(t, query)
}

// TestRowOrder_IsTheSortKeyNullsLastThenRowIndexAscending is the witness a walk cannot give: with the tiebreaker dropped SQLite still returns tied rows in rowid order, so a page sequence cannot see the order stop being total.
func TestRowOrder_IsTheSortKeyNullsLastThenRowIndexAscending(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sort RowSort
	}{
		{"num asc", RowSort{Column: "qty", Hint: RowHintNum}},
		{"num desc", RowSort{Column: "qty", Hint: RowHintNum, Desc: true}},
		{"date asc", RowSort{Column: "when", Hint: RowHintDate}},
		{"date desc", RowSort{Column: "when", Hint: RowHintDate, Desc: true}},
		{"text asc", RowSort{Column: "name"}},
		{"text desc", RowSort{Column: "name", Desc: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := "ASC"
			wantASC, wantDESC := 2, 0
			if tc.sort.Desc {
				dir = "DESC"
				wantASC, wantDESC = 1, 1
			}
			shape := regexp.MustCompile(`(?s)^.+ ` + dir + ` NULLS LAST, sheet_rows\.row_index ASC$`)
			for driver, order := range map[string]string{
				"postgres": pgRenderedOrderBy(t, &tc.sort),
				"sqlite":   sqliteRenderedOrderBy(t, &tc.sort),
			} {
				assert.Regexp(t, shape, order, "%s: the sort key carries NULLS LAST and row_index breaks every tie", driver)
				assert.Equal(t, 1, strings.Count(order, "NULLS LAST"), "%s: %s", driver, order)
				assert.Equal(t, 0, strings.Count(order, "NULLS FIRST"), "%s: %s", driver, order)
				assert.Equal(t, wantASC, strings.Count(order, " ASC"), "%s: %s", driver, order)
				assert.Equal(t, wantDESC, strings.Count(order, " DESC"), "%s: %s", driver, order)
			}
		})
	}
}

func TestRowOrder_UnsortedIsRowIndexAscendingAlone(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "sheet_rows.row_index ASC", pgRenderedOrderBy(t, nil))
	assert.Equal(t, "sheet_rows.row_index ASC", sqliteRenderedOrderBy(t, nil))
}
