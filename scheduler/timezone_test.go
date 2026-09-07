package scheduler_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/scheduler"
)

func TestFixedLocation(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)

	tests := []struct {
		name string
		loc  *time.Location
		want string
	}{
		{"named zone", jkt, "Asia/Jakarta"},
		{"nil defaults to UTC", nil, "UTC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := scheduler.FixedLocation(tt.loc)
			require.Equal(t, tt.want, f("any-job").String())
			require.Equal(t, tt.want, f("another-job").String(), "a fixed resolver ignores the job name")
		})
	}
}

func TestUsesWallClock(t *testing.T) {
	tests := []struct {
		name string
		s    scheduler.Schedule
		want bool
	}{
		{"cron is wall-clock", scheduler.MustCronExpr("0 */6 * * *", time.UTC), true},
		{"daily is wall-clock", scheduler.MustDailyAt(3, 30, time.UTC), true},
		{"interval is not", scheduler.MustEveryInterval(time.Minute, 0), false},
		{"nil is not", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, scheduler.UsesWallClock(tt.s))
		})
	}
}
