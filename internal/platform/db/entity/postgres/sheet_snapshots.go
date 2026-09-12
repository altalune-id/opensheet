package postgres

import "github.com/go-jet/jet/v2/postgres"

// SheetSnapshots is the jet binding for the sheet_snapshots table.
type SheetSnapshots struct {
	postgres.Table

	SheetID   postgres.ColumnString
	Tab       postgres.ColumnString
	ETag      postgres.ColumnString
	Payload   postgres.ColumnBytea
	FetchedAt postgres.ColumnTimestampz
	ExpiresAt postgres.ColumnTimestampz
	OrgID     postgres.ColumnString
	ProjectID postgres.ColumnString

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewSheetSnapshots builds the sheet_snapshots binding.
func NewSheetSnapshots(schema, tablePrefix string) *SheetSnapshots {
	if schema == "" {
		schema = "public"
	}
	var (
		sheetID   = postgres.StringColumn("sheet_id")
		tab       = postgres.StringColumn("tab")
		etag      = postgres.StringColumn("etag")
		payload   = postgres.ByteaColumn("payload")
		fetchedAt = postgres.TimestampzColumn("fetched_at")
		expiresAt = postgres.TimestampzColumn("expires_at")
		orgID     = postgres.StringColumn("org_id")
		projectID = postgres.StringColumn("project_id")
		all       = postgres.ColumnList{
			sheetID, tab, etag, payload, fetchedAt, expiresAt, orgID, projectID,
		}
		mutable = postgres.ColumnList{
			etag, payload, fetchedAt, expiresAt, orgID, projectID,
		}
	)
	return &SheetSnapshots{
		Table:          postgres.NewTable(schema, tablePrefix+"sheet_snapshots", "sheet_snapshots", all...),
		SheetID:        sheetID,
		Tab:            tab,
		ETag:           etag,
		Payload:        payload,
		FetchedAt:      fetchedAt,
		ExpiresAt:      expiresAt,
		OrgID:          orgID,
		ProjectID:      projectID,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
