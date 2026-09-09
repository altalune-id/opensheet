package spreadsheet

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteStore_ListStatementCarriesTheIDTiebreak(t *testing.T) {
	s := newSQLiteStore(nil, "opensheet_")
	stmt, _ := s.listStmt(uuid.New(), uuid.New()).Sql()
	require.Contains(t, stmt, "ORDER BY spreadsheets.created_at ASC, spreadsheets.id ASC",
		"List must emit the tiebreak; a row-order test alone passes on plan luck")
}
