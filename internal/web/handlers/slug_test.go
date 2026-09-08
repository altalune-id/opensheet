package handlers

import (
	"regexp"
	"strings"
	"testing"
)

var sheetSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func TestSlugifyTab(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"ordinary", "Rates 2026", "rates-2026"},
		{"separator run", "Q1 / Q2", "q1-q2"},
		{"trims", "  Payroll  ", "payroll"},
		{"underscores", "unit_rates", "unit-rates"},
		{"leading digit is legal", "2026", "2026"},
		{"punctuation only", "???", ""},
		{"non latin", "料金", ""},
		{"leading separator dropped", "-Rates", "rates"},
		{"trailing separator dropped", "Rates-", "rates"},
		{"truncated", strings.Repeat("A", 80), strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := slugifyTab(tc.in)
			if got != tc.want {
				t.Fatalf("slugifyTab(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got != "" && !sheetSlugRe.MatchString(got) {
				t.Fatalf("slugifyTab(%q) = %q, which sheet.validateSlug would reject", tc.in, got)
			}
		})
	}
}
