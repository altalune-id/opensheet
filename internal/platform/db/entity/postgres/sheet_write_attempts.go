package postgres

import "github.com/go-jet/jet/v2/postgres"

// SheetWriteAttempts is the jet binding for the sheet_write_attempts table.
type SheetWriteAttempts struct {
	postgres.Table

	SheetID    postgres.ColumnString
	Tab        postgres.ColumnString
	IdemKey    postgres.ColumnString
	BodyHash   postgres.ColumnString
	Payload    postgres.ColumnBytea
	Done       postgres.ColumnBool
	ClaimToken postgres.ColumnString
	CreatedAt  postgres.ColumnTimestampz
	ExpiresAt  postgres.ColumnTimestampz
	OrgID      postgres.ColumnString
	ProjectID  postgres.ColumnString

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewSheetWriteAttempts builds the sheet_write_attempts binding.
func NewSheetWriteAttempts(schema, tablePrefix string) *SheetWriteAttempts {
	if schema == "" {
		schema = "public"
	}
	var (
		sheetID    = postgres.StringColumn("sheet_id")
		tab        = postgres.StringColumn("tab")
		idemKey    = postgres.StringColumn("idem_key")
		bodyHash   = postgres.StringColumn("body_hash")
		payload    = postgres.ByteaColumn("payload")
		done       = postgres.BoolColumn("done")
		claimToken = postgres.StringColumn("claim_token")
		createdAt  = postgres.TimestampzColumn("created_at")
		expiresAt  = postgres.TimestampzColumn("expires_at")
		orgID      = postgres.StringColumn("org_id")
		projectID  = postgres.StringColumn("project_id")
		all        = postgres.ColumnList{
			sheetID, tab, idemKey, bodyHash, payload, done, claimToken,
			createdAt, expiresAt, orgID, projectID,
		}
		mutable = postgres.ColumnList{
			bodyHash, payload, done, claimToken, createdAt, expiresAt, orgID, projectID,
		}
	)
	return &SheetWriteAttempts{
		Table: postgres.NewTable(
			schema, tablePrefix+"sheet_write_attempts", "sheet_write_attempts", all...,
		),
		SheetID:        sheetID,
		Tab:            tab,
		IdemKey:        idemKey,
		BodyHash:       bodyHash,
		Payload:        payload,
		Done:           done,
		ClaimToken:     claimToken,
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
		OrgID:          orgID,
		ProjectID:      projectID,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
