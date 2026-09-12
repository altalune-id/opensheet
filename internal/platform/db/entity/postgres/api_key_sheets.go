package postgres

import "github.com/go-jet/jet/v2/postgres"

// APIKeySheets is the jet binding for the api_key_sheets join table.
type APIKeySheets struct {
	postgres.Table

	APIKeyID postgres.ColumnString
	SheetID  postgres.ColumnString
	OrgID    postgres.ColumnString

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewAPIKeySheets builds the api_key_sheets binding.
func NewAPIKeySheets(schema, tablePrefix string) *APIKeySheets {
	if schema == "" {
		schema = "public"
	}
	var (
		apiKeyID = postgres.StringColumn("api_key_id")
		sheetID  = postgres.StringColumn("sheet_id")
		orgID    = postgres.StringColumn("org_id")
		all      = postgres.ColumnList{apiKeyID, sheetID, orgID}
		mutable  = postgres.ColumnList{orgID}
	)
	return &APIKeySheets{
		Table:          postgres.NewTable(schema, tablePrefix+"api_key_sheets", "api_key_sheets", all...),
		APIKeyID:       apiKeyID,
		SheetID:        sheetID,
		OrgID:          orgID,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
