package slug_test

import (
	"errors"
	"regexp"
	"testing"

	"altalune.id/opensheet/slug"
)

var shape = regexp.MustCompile(`^[a-z]{3,8}-[a-z]{3,8}-[1-9][0-9]{3}$`)

func TestGenerate_Shape(t *testing.T) {
	for range 2000 {
		got := slug.Generate()
		if !shape.MatchString(got) {
			t.Fatalf("Generate() = %q, want <adjective>-<noun>-<four digits>", got)
		}
	}
}

func TestGenerate_Varies(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		seen[slug.Generate()] = true
	}
	if len(seen) < 100 {
		t.Fatalf("200 calls produced only %d distinct slugs", len(seen))
	}
}

func TestCombinations(t *testing.T) {
	if got := slug.Combinations(); got < 1_000_000 {
		t.Fatalf("Combinations() = %d, want at least 1e6 to keep collisions negligible", got)
	}
}

var errTaken = errors.New("taken")

func isTaken(err error) bool { return errors.Is(err, errTaken) }

func TestRetry_ReturnsTheFirstFreeCandidate(t *testing.T) {
	calls := 0
	got, err := slug.Retry(slug.MaxAttempts, isTaken, func(candidate string) (string, error) {
		calls++
		if calls < slug.MaxAttempts {
			return "", errTaken
		}
		return candidate, nil
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if calls != slug.MaxAttempts {
		t.Fatalf("calls = %d, want %d", calls, slug.MaxAttempts)
	}
	if !shape.MatchString(got) {
		t.Fatalf("Retry passed %q, want a generated slug", got)
	}
}

func TestRetry_GivesUpWithTheLastTakenError(t *testing.T) {
	calls := 0
	_, err := slug.Retry(slug.MaxAttempts, isTaken, func(string) (string, error) {
		calls++
		return "", errTaken
	})
	if !isTaken(err) {
		t.Fatalf("err = %v, want the taken error", err)
	}
	if calls != slug.MaxAttempts {
		t.Fatalf("calls = %d, want %d", calls, slug.MaxAttempts)
	}
}

func TestRetry_StopsOnAnyOtherError(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	_, err := slug.Retry(slug.MaxAttempts, isTaken, func(string) (string, error) {
		calls++
		return "", boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want boom after 1", err, calls)
	}
}

func TestRetry_TriesAtLeastOnce(t *testing.T) {
	calls := 0
	if _, err := slug.Retry(0, isTaken, func(c string) (string, error) {
		calls++
		return c, nil
	}); err != nil || calls != 1 {
		t.Fatalf("Retry(0) = %v after %d calls, want success after 1", err, calls)
	}
}
