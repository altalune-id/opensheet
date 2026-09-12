package gsheet

import (
	"regexp"
	"strconv"
	"strings"
)

// ColumnLabel returns the bijective base-26 A1 column label for a 1-based column number.
func ColumnLabel(n int) string {
	if n < 1 {
		return "A"
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('A' + n%26)}, out...)
		n /= 26
	}
	return string(out)
}

var a1Cell = regexp.MustCompile(`^[A-Z]{1,3}([1-9]\d{0,6})$`)

// spanStartRow returns the 1-based row an A1 range starts at, such as 7 for "'My Sheet'!A7:C9".
func spanStartRow(a1 string) (int, error) {
	span := a1
	if i := strings.LastIndex(span, "!"); i >= 0 {
		span = span[i+1:]
	}
	if i := strings.Index(span, ":"); i >= 0 {
		span = span[:i]
	}
	m := a1Cell.FindStringSubmatch(span)
	if m == nil {
		return 0, &AppendRangeError{Range: a1}
	}
	row, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, &AppendRangeError{Range: a1}
	}
	return row, nil
}
