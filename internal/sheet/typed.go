package sheet

import (
	"regexp"
	"strconv"
)

const (
	// NumPattern is the shape half of the num grammar, spelled so Go and Postgres read it identically.
	NumPattern = `^-?[0-9]{1,15}(\.[0-9]{1,15})?$`
	// DatePattern is the whole date grammar, spelled so Go and Postgres read it identically.
	DatePattern = `^([0-9]{4}-[0-9]{2}-[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)$`
	// NumSignificanceLimit is the most significant digits the num grammar admits.
	NumSignificanceLimit = 15
)

var (
	//nolint:gocritic // [0-9] rather than \d is deliberate: Postgres reads the same pattern, where \d is locale-dependent.
	numShape  = regexp.MustCompile(NumPattern)
	dateShape = regexp.MustCompile(DatePattern)
)

// ParseNum returns v as a float when it matches the num grammar, and false otherwise.
func ParseNum(v string) (float64, bool) {
	if !numShape.MatchString(v) {
		return 0, false
	}
	if numSignificantDigits(v) > NumSignificanceLimit {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// MatchesDateShape reports whether v matches the date grammar. No calendar validation is performed.
func MatchesDateShape(v string) bool {
	return dateShape.MatchString(v)
}

// numSignificantDigits counts v's significant digits: leading zeros do not count, trailing zeros do.
func numSignificantDigits(v string) int {
	n := 0
	seen := false
	for i := range len(v) {
		c := v[i]
		if c < '0' || c > '9' {
			continue
		}
		if !seen && c == '0' {
			continue
		}
		seen = true
		n++
	}
	return n
}
