package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// APIKeySheets is the jet binding for the api_key_sheets join table.
type APIKeySheets struct {
	sqlite.Table

	APIKeyID sqlite.ColumnString
	SheetID  sqlite.ColumnString
	OrgID    sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewAPIKeySheets builds the api_key_sheets binding.
func NewAPIKeySheets(tablePrefix string) *APIKeySheets {
	var (
		apiKeyID = sqlite.StringColumn("api_key_id")
		sheetID  = sqlite.StringColumn("sheet_id")
		orgID    = sqlite.StringColumn("org_id")
		all      = sqlite.ColumnList{apiKeyID, sheetID, orgID}
		mutable  = sqlite.ColumnList{orgID}
	)
	return &APIKeySheets{
		Table:          sqlite.NewTable("", tablePrefix+"api_key_sheets", "api_key_sheets", all...),
		APIKeyID:       apiKeyID,
		SheetID:        sheetID,
		OrgID:          orgID,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
