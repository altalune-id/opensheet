package sqlite

import "time"

// SQLiteTime renders t for a SQLite TEXT column, zero-padded so text comparison equals
// chronological comparison; RFC3339Nano trims trailing zeros, so ".2367Z" sorts after ".236756Z".
func SQLiteTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}
