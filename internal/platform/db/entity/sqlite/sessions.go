package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Sessions is the jet binding for the sessions table.
type Sessions struct {
	sqlite.Table

	SID       sqlite.ColumnString
	UserID    sqlite.ColumnString
	Payload   sqlite.ColumnBlob
	ExpiresAt sqlite.ColumnString
	CreatedAt sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewSessions builds the sessions binding. tablePrefix matches DB.TablePrefix (e.g. "opensheet_").
func NewSessions(tablePrefix string) *Sessions {
	var (
		sid       = sqlite.StringColumn("sid")
		userID    = sqlite.StringColumn("user_id")
		payload   = sqlite.BlobColumn("payload")
		expiresAt = sqlite.StringColumn("expires_at")
		createdAt = sqlite.StringColumn("created_at")
		all       = sqlite.ColumnList{sid, userID, payload, expiresAt, createdAt}
	)
	return &Sessions{
		Table:      sqlite.NewTable("", tablePrefix+"sessions", "sessions", all...),
		SID:        sid,
		UserID:     userID,
		Payload:    payload,
		ExpiresAt:  expiresAt,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
