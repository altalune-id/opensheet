package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Spreadsheets is the jet binding for the spreadsheets table.
type Spreadsheets struct {
	sqlite.Table

	ID           sqlite.ColumnString
	OrgID        sqlite.ColumnString
	ProjectID    sqlite.ColumnString
	CredentialID sqlite.ColumnString
	GoogleFileID sqlite.ColumnString
	Title        sqlite.ColumnString
	Writable     sqlite.ColumnInteger
	CreatedAt    sqlite.ColumnString
	UpdatedAt    sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewSpreadsheets builds the spreadsheets binding.
func NewSpreadsheets(tablePrefix string) *Spreadsheets {
	var (
		id           = sqlite.StringColumn("id")
		orgID        = sqlite.StringColumn("org_id")
		projectID    = sqlite.StringColumn("project_id")
		credentialID = sqlite.StringColumn("credential_id")
		googleFileID = sqlite.StringColumn("google_file_id")
		title        = sqlite.StringColumn("title")
		writable     = sqlite.IntegerColumn("writable")
		createdAt    = sqlite.StringColumn("created_at")
		updatedAt    = sqlite.StringColumn("updated_at")
		all          = sqlite.ColumnList{
			id, orgID, projectID, credentialID, googleFileID, title, writable, createdAt, updatedAt,
		}
		mutable = sqlite.ColumnList{
			orgID, projectID, credentialID, googleFileID, title, writable, updatedAt,
		}
	)
	return &Spreadsheets{
		Table:          sqlite.NewTable("", tablePrefix+"spreadsheets", "spreadsheets", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		CredentialID:   credentialID,
		GoogleFileID:   googleFileID,
		Title:          title,
		Writable:       writable,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
