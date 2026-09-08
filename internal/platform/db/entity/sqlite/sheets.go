package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Sheets is the jet binding for the sheets table.
type Sheets struct {
	sqlite.Table

	ID            sqlite.ColumnString
	OrgID         sqlite.ColumnString
	ProjectID     sqlite.ColumnString
	SpreadsheetID sqlite.ColumnString
	Tab           sqlite.ColumnString
	Slug          sqlite.ColumnString
	Visibility    sqlite.ColumnString
	CacheTTLSecs  sqlite.ColumnInteger
	Writable      sqlite.ColumnInteger
	CreatedAt     sqlite.ColumnString
	UpdatedAt     sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewSheets builds the sheets binding.
func NewSheets(tablePrefix string) *Sheets {
	var (
		id            = sqlite.StringColumn("id")
		orgID         = sqlite.StringColumn("org_id")
		projectID     = sqlite.StringColumn("project_id")
		spreadsheetID = sqlite.StringColumn("spreadsheet_id")
		tab           = sqlite.StringColumn("tab")
		slug          = sqlite.StringColumn("slug")
		visibility    = sqlite.StringColumn("visibility")
		cacheTTLSecs  = sqlite.IntegerColumn("cache_ttl_secs")
		writable      = sqlite.IntegerColumn("writable")
		createdAt     = sqlite.StringColumn("created_at")
		updatedAt     = sqlite.StringColumn("updated_at")
		all           = sqlite.ColumnList{
			id, orgID, projectID, spreadsheetID, tab, slug,
			visibility, cacheTTLSecs, writable, createdAt, updatedAt,
		}
		mutable = sqlite.ColumnList{
			orgID, projectID, spreadsheetID, tab, slug,
			visibility, cacheTTLSecs, writable, updatedAt,
		}
	)
	return &Sheets{
		Table:          sqlite.NewTable("", tablePrefix+"sheets", "sheets", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		SpreadsheetID:  spreadsheetID,
		Tab:            tab,
		Slug:           slug,
		Visibility:     visibility,
		CacheTTLSecs:   cacheTTLSecs,
		Writable:       writable,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
