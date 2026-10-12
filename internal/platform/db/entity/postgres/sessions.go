package postgres

import "github.com/go-jet/jet/v2/postgres"

// Sessions is the jet binding for the sessions table.
type Sessions struct {
	postgres.Table

	SID       postgres.ColumnString
	UserID    postgres.ColumnString
	Payload   postgres.ColumnBytea
	ExpiresAt postgres.ColumnTimestampz
	CreatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewSessions builds the sessions binding.
func NewSessions(schema, tablePrefix string) *Sessions {
	if schema == "" {
		schema = "public"
	}
	var (
		sid       = postgres.StringColumn("sid")
		userID    = postgres.StringColumn("user_id")
		payload   = postgres.ByteaColumn("payload")
		expiresAt = postgres.TimestampzColumn("expires_at")
		createdAt = postgres.TimestampzColumn("created_at")
		all       = postgres.ColumnList{sid, userID, payload, expiresAt, createdAt}
	)
	return &Sessions{
		Table:      postgres.NewTable(schema, tablePrefix+"sessions", "sessions", all...),
		SID:        sid,
		UserID:     userID,
		Payload:    payload,
		ExpiresAt:  expiresAt,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
