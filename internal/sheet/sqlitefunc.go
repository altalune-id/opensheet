package sheet

import (
	"database/sql/driver"

	"modernc.org/sqlite"
)

// NOTE: registration is per connection and once-only, so it must beat the first SQLite connection — no package may open a SQLite connection from its own init().
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("opensheet_num", 1, sqliteNum)
	sqlite.MustRegisterDeterministicScalarFunction("opensheet_date", 1, sqliteDate)
}

func sqliteNum(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	v, ok := args[0].(string)
	if !ok {
		return nil, nil
	}
	f, ok := ParseNum(v)
	if !ok {
		return nil, nil
	}
	return f, nil
}

func sqliteDate(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	v, ok := args[0].(string)
	if !ok {
		return nil, nil
	}
	if !MatchesDateShape(v) {
		return nil, nil
	}
	return v, nil
}
