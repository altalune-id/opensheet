package scheduler

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// NOTE: built explicitly because cron.ParseStandard rejects the optional seconds column.
//
//nolint:gochecknoglobals // Stateless parser fixture; safe for concurrent use.
var cronParser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// CronExpr parses expr into a Schedule evaluated in loc; nil loc means UTC.
func CronExpr(expr string, loc *time.Location) (Schedule, error) {
	if expr == "" {
		return nil, errors.New("scheduler.CronExpr: expression is required")
	}
	if loc == nil {
		loc = time.UTC
	}
	inner, err := cronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("scheduler.CronExpr: parse %q: %w", expr, err)
	}
	return cronExpr{expr: expr, loc: loc, inner: inner}, nil
}

// MustCronExpr is CronExpr that panics on a parse error; use only with constants.
func MustCronExpr(expr string, loc *time.Location) Schedule {
	s, err := CronExpr(expr, loc)
	if err != nil {
		panic(err)
	}
	return s
}

type cronExpr struct {
	expr  string
	loc   *time.Location
	inner cron.Schedule
}

func (c cronExpr) Next(now time.Time) time.Time { return c.inner.Next(now.In(c.loc)) }

func (c cronExpr) String() string { return fmt.Sprintf("cron %q %s", c.expr, c.loc.String()) }
