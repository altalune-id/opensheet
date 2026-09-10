package gsheet

import "testing"

func TestColumnLetter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want string
	}{
		{1, "A"}, {2, "B"}, {25, "Y"}, {26, "Z"},
		{27, "AA"}, {28, "AB"}, {51, "AY"}, {52, "AZ"},
		{53, "BA"}, {702, "ZZ"}, {703, "AAA"},
	}
	for _, tc := range cases {
		if got := ColumnLabel(tc.n); got != tc.want {
			t.Errorf("ColumnLabel(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestColumnLetter_NonPositiveFallsBackToTheFirstColumn(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1} {
		if got := ColumnLabel(n); got != "A" {
			t.Errorf("ColumnLabel(%d) = %q, want %q", n, got, "A")
		}
	}
}
