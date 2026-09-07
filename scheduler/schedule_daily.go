package scheduler

import (
	"fmt"
	"time"
)

// DailyAt fires once per day at hour:minute in loc; nil loc means UTC. NOTE: DST follows time.Date semantics.
func DailyAt(hour, minute int, loc *time.Location) (Schedule, error) {
	if hour < 0 || hour > 23 {
		return nil, fmt.Errorf("scheduler.DailyAt: hour out of range [0,23], got %d", hour)
	}
	if minute < 0 || minute > 59 {
		return nil, fmt.Errorf("scheduler.DailyAt: minute out of range [0,59], got %d", minute)
	}
	if loc == nil {
		loc = time.UTC
	}
	return dailyAt{hour: hour, minute: minute, loc: loc}, nil
}

// MustDailyAt is DailyAt that panics on invalid input; use only with constants.
func MustDailyAt(hour, minute int, loc *time.Location) Schedule {
	s, err := DailyAt(hour, minute, loc)
	if err != nil {
		panic(err)
	}
	return s
}

type dailyAt struct {
	hour, minute int
	loc          *time.Location
}

func (d dailyAt) Next(now time.Time) time.Time {
	local := now.In(d.loc)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), d.hour, d.minute, 0, 0, d.loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func (d dailyAt) String() string {
	return fmt.Sprintf("daily at %02d:%02d %s", d.hour, d.minute, d.loc.String())
}
