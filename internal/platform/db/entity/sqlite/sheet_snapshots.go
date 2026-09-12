package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// SheetSnapshots is the jet binding for the sheet_snapshots table.
type SheetSnapshots struct {
	sqlite.Table

	SheetID   sqlite.ColumnString
	Tab       sqlite.ColumnString
	ETag      sqlite.ColumnString
	Payload   sqlite.ColumnBlob
	FetchedAt sqlite.ColumnString
	ExpiresAt sqlite.ColumnString
	OrgID     sqlite.ColumnString
	ProjectID sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewSheetSnapshots builds the sheet_snapshots binding.
func NewSheetSnapshots(tablePrefix string) *SheetSnapshots {
	var (
		sheetID   = sqlite.StringColumn("sheet_id")
		tab       = sqlite.StringColumn("tab")
		etag      = sqlite.StringColumn("etag")
		payload   = sqlite.BlobColumn("payload")
		fetchedAt = sqlite.StringColumn("fetched_at")
		expiresAt = sqlite.StringColumn("expires_at")
		orgID     = sqlite.StringColumn("org_id")
		projectID = sqlite.StringColumn("project_id")
		all       = sqlite.ColumnList{
			sheetID, tab, etag, payload, fetchedAt, expiresAt, orgID, projectID,
		}
		mutable = sqlite.ColumnList{
			etag, payload, fetchedAt, expiresAt, orgID, projectID,
		}
	)
	return &SheetSnapshots{
		Table:          sqlite.NewTable("", tablePrefix+"sheet_snapshots", "sheet_snapshots", all...),
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
