package scheduler_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/scheduler"
)

func TestEveryInterval_Validation(t *testing.T) {
	tests := []struct {
		name      string
		d, jitter time.Duration
		wantErr   bool
	}{
		{"positive interval, no jitter", time.Minute, 0, false},
		{"positive interval and jitter", time.Minute, time.Second, false},
		{"zero interval", 0, 0, true},
		{"negative interval", -time.Second, 0, true},
		{"negative jitter", time.Minute, -time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := scheduler.EveryInterval(tt.d, tt.jitter)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, s)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, s)
		})
	}
}

func TestEveryInterval_JitterOnlyOnFirstTick(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := scheduler.MustEveryInterval(time.Minute, 10*time.Second)

	first := s.Next(now)
	require.True(t, !first.Before(now.Add(time.Minute)), "first tick must be at least the interval")
	require.True(t, first.Before(now.Add(70*time.Second)), "first tick must be within interval+jitter")

	second := s.Next(now)
	require.Equal(t, now.Add(time.Minute), second, "subsequent ticks carry no jitter")
}

func TestMustEveryInterval_PanicsOnZero(t *testing.T) {
	require.Panics(t, func() { scheduler.MustEveryInterval(0, 0) })
}

func TestEveryInterval_String(t *testing.T) {
	require.Equal(t, "every 1m0s", scheduler.MustEveryInterval(time.Minute, 0).String())
	require.Equal(t, "every 1m0s (first-tick jitter 10s)", scheduler.MustEveryInterval(time.Minute, 10*time.Second).String())
}
