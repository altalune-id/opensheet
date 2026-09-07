package scheduler

import "time"

// LocationFunc resolves the location a named job's wall-clock schedule uses.
type LocationFunc = func(jobName string) *time.Location

// FixedLocation returns a LocationFunc resolving every job to loc; nil means UTC.
func FixedLocation(loc *time.Location) LocationFunc {
	if loc == nil {
		loc = time.UTC
	}
	return func(string) *time.Location { return loc }
}

// UsesWallClock reports whether s consults a time.Location, which interval schedules do not.
func UsesWallClock(s Schedule) bool {
	switch s.(type) {
	case dailyAt, cronExpr:
		return true
	default:
		return false
	}
}
