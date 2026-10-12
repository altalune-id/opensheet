package apperror_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"altalune.id/opensheet/internal/apperror"
)

func TestTruncateCause(t *testing.T) {
	cases := []struct {
		name  string
		cause string
		limit int
		want  string
	}{
		{"empty", "", 10, ""},
		{"exactly at limit", "abcde", 5, "abcde"},
		{"over limit ascii", "abcdefghij", 5, "abcde"},
		{"limit zero", "abc", 0, ""},
		{"limit negative", "abc", -1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := apperror.TruncateCause(tc.cause, tc.limit)
			if got != tc.want {
				t.Errorf("TruncateCause(%q, %d) = %q, want %q", tc.cause, tc.limit, got, tc.want)
			}
		})
	}
}

func TestTruncateCause_CutSplittingAMultiByteRune(t *testing.T) {
	limit := 9
	cause := strings.Repeat("a", limit-1) + "é" + strings.Repeat("b", 8)
	if utf8.ValidString(cause[:limit]) {
		t.Fatalf("fixture does not straddle the cut at limit %d", limit)
	}

	got := apperror.TruncateCause(cause, limit)
	if !utf8.ValidString(got) {
		t.Errorf("TruncateCause(%q, %d) = %q, not valid UTF-8", cause, limit, got)
	}
	if len(got) > limit {
		t.Errorf("TruncateCause(%q, %d) = %q, len %d > limit %d", cause, limit, got, len(got), limit)
	}
}
