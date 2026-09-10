package postgres

import "github.com/go-jet/jet/v2/postgres"

// Sheets is the jet binding for the sheets table.
type Sheets struct {
	postgres.Table

	ID             postgres.ColumnString
	OrgID          postgres.ColumnString
	ProjectID      postgres.ColumnString
	SpreadsheetID  postgres.ColumnString
	Tab            postgres.ColumnString
	Slug           postgres.ColumnString
	Visibility     postgres.ColumnString
	CacheTTLSecs   postgres.ColumnInteger
	Writable       postgres.ColumnBool
	CreatedAt      postgres.ColumnTimestampz
	UpdatedAt      postgres.ColumnTimestampz
	Generation     postgres.ColumnInteger
	ValidatedAt    postgres.ColumnTimestampz
	ContractOK     postgres.ColumnBool
	ContractReason postgres.ColumnString
	SoftDelete     postgres.ColumnBool

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewSheets builds the sheets binding.
func NewSheets(schema, tablePrefix string) *Sheets {
	if schema == "" {
		schema = "public"
	}
	var (
		id             = postgres.StringColumn("id")
		orgID          = postgres.StringColumn("org_id")
		projectID      = postgres.StringColumn("project_id")
		spreadsheetID  = postgres.StringColumn("spreadsheet_id")
		tab            = postgres.StringColumn("tab")
		slug           = postgres.StringColumn("slug")
		visibility     = postgres.StringColumn("visibility")
		cacheTTLSecs   = postgres.IntegerColumn("cache_ttl_secs")
		writable       = postgres.BoolColumn("writable")
		createdAt      = postgres.TimestampzColumn("created_at")
		updatedAt      = postgres.TimestampzColumn("updated_at")
		generation     = postgres.IntegerColumn("generation")
		validatedAt    = postgres.TimestampzColumn("validated_at")
		contractOK     = postgres.BoolColumn("contract_ok")
		contractReason = postgres.StringColumn("contract_reason")
		softDelete     = postgres.BoolColumn("soft_delete")
		all            = postgres.ColumnList{
			id, orgID, projectID, spreadsheetID, tab, slug,
			visibility, cacheTTLSecs, writable, createdAt, updatedAt,
			generation, validatedAt, contractOK, contractReason, softDelete,
		}
		// NOTE: generation is deliberately absent — an upsert must never reset the staleness guard or the write lock.
		mutable = postgres.ColumnList{
			orgID, projectID, spreadsheetID, tab, slug,
			visibility, cacheTTLSecs, writable, updatedAt,
			validatedAt, contractOK, contractReason, softDelete,
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
		Writable:       writable,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		Generation:     generation,
		ValidatedAt:    validatedAt,
		ContractOK:     contractOK,
		ContractReason: contractReason,
		SoftDelete:     softDelete,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
