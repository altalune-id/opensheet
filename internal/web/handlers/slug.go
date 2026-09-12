package handlers

import (
	"strings"

	"altalune.id/opensheet/internal/sheet"
)

func slugifyTab(tab string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(tab)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case r == ' ' || r == '_' || r == '-' || r == '/':
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > sheet.SlugMaxLen {
		out = strings.Trim(out[:sheet.SlugMaxLen], "-")
	}
	return out
}
