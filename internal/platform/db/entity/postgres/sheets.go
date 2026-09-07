package postgres

import "github.com/go-jet/jet/v2/postgres"

// Sheets is the jet binding for the sheets table.
type Sheets struct {
	postgres.Table

	ID            postgres.ColumnString
	OrgID         postgres.ColumnString
	ProjectID     postgres.ColumnString
	SpreadsheetID postgres.ColumnString
	Tab           postgres.ColumnString
	Slug          postgres.ColumnString
	Visibility    postgres.ColumnString
	CacheTTLSecs  postgres.ColumnInteger
	CreatedAt     postgres.ColumnTimestampz
	UpdatedAt     postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewSheets builds the sheets binding.
func NewSheets(schema, tablePrefix string) *Sheets {
	if schema == "" {
		schema = "public"
	}
	var (
		id            = postgres.StringColumn("id")
		orgID         = postgres.StringColumn("org_id")
		projectID     = postgres.StringColumn("project_id")
		spreadsheetID = postgres.StringColumn("spreadsheet_id")
		tab           = postgres.StringColumn("tab")
		slug          = postgres.StringColumn("slug")
		visibility    = postgres.StringColumn("visibility")
		cacheTTLSecs  = postgres.IntegerColumn("cache_ttl_secs")
		createdAt     = postgres.TimestampzColumn("created_at")
		updatedAt     = postgres.TimestampzColumn("updated_at")
		all           = postgres.ColumnList{
			id, orgID, projectID, spreadsheetID, tab, slug,
			visibility, cacheTTLSecs, createdAt, updatedAt,
		}
		mutable = postgres.ColumnList{
			orgID, projectID, spreadsheetID, tab, slug,
			visibility, cacheTTLSecs, updatedAt,
		}
	)
	return &Sheets{
		Table:          postgres.NewTable(schema, tablePrefix+"sheets", "sheets", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		SpreadsheetID:  spreadsheetID,
		Tab:            tab,
		Slug:           slug,
		Visibility:     visibility,
		CacheTTLSecs:   cacheTTLSecs,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
