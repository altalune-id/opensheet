package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// SheetRows is the jet binding for the sheet_rows table.
type SheetRows struct {
	sqlite.Table

	SheetID   sqlite.ColumnString
	Tab       sqlite.ColumnString
	RowID     sqlite.ColumnString
	RowIndex  sqlite.ColumnInteger
	Data      sqlite.ColumnString
	DeletedAt sqlite.ColumnString
	OrgID     sqlite.ColumnString
	ProjectID sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewSheetRows builds the sheet_rows binding.
func NewSheetRows(tablePrefix string) *SheetRows {
	var (
		sheetID   = sqlite.StringColumn("sheet_id")
		tab       = sqlite.StringColumn("tab")
		rowID     = sqlite.StringColumn("row_id")
		rowIndex  = sqlite.IntegerColumn("row_index")
		data      = sqlite.StringColumn("data")
		deletedAt = sqlite.StringColumn("deleted_at")
		orgID     = sqlite.StringColumn("org_id")
		projectID = sqlite.StringColumn("project_id")
		all       = sqlite.ColumnList{
			sheetID, tab, rowID, rowIndex, data, deletedAt, orgID, projectID,
		}
		mutable = sqlite.ColumnList{
			rowIndex, data, deletedAt, orgID, projectID,
		}
	)
	return &SheetRows{
		Table:          sqlite.NewTable("", tablePrefix+"sheet_rows", "sheet_rows", all...),
		SheetID:        sheetID,
		Tab:            tab,
		RowID:          rowID,
		RowIndex:       rowIndex,
		Data:           data,
		DeletedAt:      deletedAt,
		OrgID:          orgID,
		ProjectID:      projectID,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
