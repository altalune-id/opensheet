package scheduler_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/scheduler"
)

func TestDailyAt_Validation(t *testing.T) {
	for _, tt := range []struct {
		name         string
		hour, minute int
		wantErr      bool
	}{
		{"midnight", 0, 0, false},
		{"end of day", 23, 59, false},
		{"hour too high", 24, 0, true},
		{"hour negative", -1, 0, true},
		{"minute too high", 0, 60, true},
		{"minute negative", 0, -1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scheduler.DailyAt(tt.hour, tt.minute, time.UTC)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDailyAt_Next(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)

	tests := []struct {
		name string
		now  time.Time
		loc  *time.Location
		want time.Time
	}{
		{
			name: "later today",
			now:  time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC),
			loc:  time.UTC,
			want: time.Date(2026, 3, 1, 3, 30, 0, 0, time.UTC),
		},
		{
			name: "already passed rolls to tomorrow",
			now:  time.Date(2026, 3, 1, 4, 0, 0, 0, time.UTC),
			loc:  time.UTC,
			want: time.Date(2026, 3, 2, 3, 30, 0, 0, time.UTC),
		},
		{
			name: "nil location defaults to UTC",
			now:  time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC),
			loc:  nil,
			want: time.Date(2026, 3, 1, 3, 30, 0, 0, time.UTC),
		},
		{
			name: "non-UTC location",
			now:  time.Date(2026, 3, 1, 0, 0, 0, 0, jkt),
			loc:  jkt,
			want: time.Date(2026, 3, 1, 3, 30, 0, 0, jkt),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := scheduler.MustDailyAt(3, 30, tt.loc)
			require.True(t, s.Next(tt.now).Equal(tt.want), "got %s want %s", s.Next(tt.now), tt.want)
		})
	}
}

func TestDailyAt_DSTSpringForward(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	// 2026-03-08 02:30 EST does not exist; time.Date normalizes it into EDT.
	s := scheduler.MustDailyAt(2, 30, ny)
	got := s.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, ny))
	require.True(t, got.After(time.Date(2026, 3, 8, 0, 0, 0, 0, ny)))
	require.Equal(t, 2026, got.Year())
}
