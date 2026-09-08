package gsheet

// columnLetter returns the bijective base-26 A1 column label for a 1-based column number.
func columnLetter(n int) string {
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
