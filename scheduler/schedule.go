// Package scheduler runs registered Jobs on wall-clock or interval schedules inside one process.
package scheduler

import "time"

// Schedule decides when a Job fires next.
type Schedule interface {
	// NOTE: the Runner calls Next concurrently across independent Schedules, so any randomness must be guarded.
	Next(now time.Time) time.Time
	String() string
}
