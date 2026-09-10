package sqlite

import (
	"testing"
	"time"
)

func TestSQLiteTime_TextOrderMatchesChronologicalOrder(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 9, 2, 12, 19, 0, time.UTC)
	times := []time.Time{
		base,
		base.Add(1 * time.Nanosecond),
		base.Add(100 * time.Microsecond),
		base.Add(236700 * time.Microsecond),
		base.Add(236756 * time.Microsecond),
		base.Add(1 * time.Second),
	}
	for i := 1; i < len(times); i++ {
		prev, cur := SQLiteTime(times[i-1]), SQLiteTime(times[i])
		if prev >= cur {
			t.Errorf("text order disagrees with time order: %q !< %q", prev, cur)
		}
	}
}

// The invariant is structural: a non-UTC input must not produce an offset that breaks ordering.
func TestSQLiteTime_NormalisesToUTC(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("X", -8*3600)
	utc := time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC)
	if got, want := SQLiteTime(utc.In(zone)), SQLiteTime(utc); got != want {
		t.Errorf("SQLiteTime(non-UTC) = %q, want %q", got, want)
	}
}
