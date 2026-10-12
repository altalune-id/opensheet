package sqlite_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	require.NoError(t, err)
	return parsed
}

func TestSQLiteTime_TextOrderMatchesChronologicalOrder(t *testing.T) {
	cases := []struct {
		name     string
		earlier  string
		later    string
		rfcAgree bool
	}{
		{
			name:     "trimmed trailing zeros",
			earlier:  "2026-09-09T02:12:19.2367Z",
			later:    "2026-09-09T02:12:19.236756Z",
			rfcAgree: false,
		},
		{
			name:     "whole second against one nanosecond",
			earlier:  "2026-09-09T02:12:19Z",
			later:    "2026-09-09T02:12:19.000000001Z",
			rfcAgree: false,
		},
		{
			name:     "distinct seconds",
			earlier:  "2026-09-09T02:12:19.500000000Z",
			later:    "2026-09-09T02:12:20.100000000Z",
			rfcAgree: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := mustParse(t, tc.earlier)
			b := mustParse(t, tc.later)
			require.True(t, a.Before(b), "fixture must be chronologically ordered")

			assert.Less(t, sqliteent.SQLiteTime(a), sqliteent.SQLiteTime(b),
				"padded text order must match chronological order")

			rfcOrdered := a.UTC().Format(time.RFC3339Nano) < b.UTC().Format(time.RFC3339Nano)
			assert.Equal(t, tc.rfcAgree, rfcOrdered,
				"RFC3339Nano text order for %q vs %q", tc.earlier, tc.later)
		})
	}
}

func TestSQLiteTime_RoundTripsThroughRFC3339Nano(t *testing.T) {
	cases := []time.Time{
		mustParse(t, "2026-09-09T02:12:19Z"),
		mustParse(t, "2026-09-09T02:12:19.000000001Z"),
		mustParse(t, "2026-09-09T02:12:19.2367Z"),
		mustParse(t, "2026-09-09T02:12:19.236756Z"),
		mustParse(t, "2026-09-09T09:12:19.5+07:00"),
		time.Unix(0, 0).UTC(),
	}

	for _, want := range cases {
		rendered := sqliteent.SQLiteTime(want)
		got, err := time.Parse(time.RFC3339Nano, rendered)
		require.NoError(t, err, "rendered %q", rendered)
		assert.True(t, want.Equal(got), "round trip of %q via %q gave %q", want, rendered, got)
		assert.Equal(t, time.UTC, got.Location())
	}
}

func TestSQLiteTime_RendersFixedWidthUTC(t *testing.T) {
	utc := sqliteent.SQLiteTime(mustParse(t, "2026-09-09T02:12:19.2367Z"))
	offset := sqliteent.SQLiteTime(mustParse(t, "2026-09-09T09:12:19.2367+07:00"))

	assert.Equal(t, "2026-09-09T02:12:19.236700000Z", utc)
	assert.Equal(t, utc, offset, "an offset input must normalize to the same UTC text")
	assert.Len(t, utc, len("2006-01-02T15:04:05.000000000Z"))
}
