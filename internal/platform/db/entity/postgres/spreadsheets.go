package postgres

import "github.com/go-jet/jet/v2/postgres"

// Spreadsheets is the jet binding for the spreadsheets table.
type Spreadsheets struct {
	postgres.Table

	ID           postgres.ColumnString
	OrgID        postgres.ColumnString
	ProjectID    postgres.ColumnString
	CredentialID postgres.ColumnString
	GoogleFileID postgres.ColumnString
	Title        postgres.ColumnString
	CreatedAt    postgres.ColumnTimestampz
	UpdatedAt    postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewSpreadsheets builds the spreadsheets binding.
func NewSpreadsheets(schema, tablePrefix string) *Spreadsheets {
	if schema == "" {
		schema = "public"
	}
	var (
		id           = postgres.StringColumn("id")
		orgID        = postgres.StringColumn("org_id")
		projectID    = postgres.StringColumn("project_id")
		credentialID = postgres.StringColumn("credential_id")
		googleFileID = postgres.StringColumn("google_file_id")
		title        = postgres.StringColumn("title")
		createdAt    = postgres.TimestampzColumn("created_at")
		updatedAt    = postgres.TimestampzColumn("updated_at")
		all          = postgres.ColumnList{
			id, orgID, projectID, credentialID, googleFileID, title, createdAt, updatedAt,
		}
		mutable = postgres.ColumnList{
			orgID, projectID, credentialID, googleFileID, title, updatedAt,
		}
	)
	return &Spreadsheets{
		Table:          postgres.NewTable(schema, tablePrefix+"spreadsheets", "spreadsheets", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		CredentialID:   credentialID,
		GoogleFileID:   googleFileID,
		Title:          title,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
