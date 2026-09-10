package postgres

import "github.com/go-jet/jet/v2/postgres"

// SheetRows is the jet binding for the sheet_rows table.
type SheetRows struct {
	postgres.Table

	SheetID   postgres.ColumnString
	Tab       postgres.ColumnString
	RowID     postgres.ColumnString
	RowIndex  postgres.ColumnInteger
	Data      postgres.ColumnString
	DeletedAt postgres.ColumnTimestampz
	OrgID     postgres.ColumnString
	ProjectID postgres.ColumnString

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewSheetRows builds the sheet_rows binding.
func NewSheetRows(schema, tablePrefix string) *SheetRows {
	if schema == "" {
		schema = "public"
	}
	var (
		sheetID   = postgres.StringColumn("sheet_id")
		tab       = postgres.StringColumn("tab")
		rowID     = postgres.StringColumn("row_id")
		rowIndex  = postgres.IntegerColumn("row_index")
		data      = postgres.StringColumn("data")
		deletedAt = postgres.TimestampzColumn("deleted_at")
		orgID     = postgres.StringColumn("org_id")
		projectID = postgres.StringColumn("project_id")
		all       = postgres.ColumnList{
			sheetID, tab, rowID, rowIndex, data, deletedAt, orgID, projectID,
		}
		mutable = postgres.ColumnList{
			rowIndex, data, deletedAt, orgID, projectID,
		}
	)
	return &SheetRows{
		Table:          postgres.NewTable(schema, tablePrefix+"sheet_rows", "sheet_rows", all...),
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
