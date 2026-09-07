package i18n

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// Locale is a BCP-47 language tag the app renders in.
type Locale string

//revive:disable:var-naming BCP-47 language subtags render more readably as MixedCase constants than as SCREAMING (IdID/JaJP over IDID/JAJP).
const (
	EnUS Locale = "en-US"
	IdID Locale = "id-ID"
	MsMY Locale = "ms-MY"
	JaJP Locale = "ja-JP"
	ArSA Locale = "ar-SA"
)

//revive:enable:var-naming

// String returns the BCP-47 tag.
func (l Locale) String() string { return string(l) }

// NativeName returns the language's endonym for a language picker.
func (l Locale) NativeName() string {
	if n, ok := nativeNames[l]; ok {
		return n
	}
	return string(l)
}

var nativeNames = map[Locale]string{ //nolint:gochecknoglobals // static lookup table
	EnUS: "English",
	IdID: "Bahasa Indonesia",
	MsMY: "Bahasa Melayu",
	JaJP: "日本語",
	ArSA: "العربية",
}

// Dir returns "rtl" for right-to-left locales, "ltr" otherwise.
func (l Locale) Dir() string {
	if l.IsRTL() {
		return "rtl"
	}
	return "ltr"
}

// IsRTL reports whether the locale is right-to-left.
func (l Locale) IsRTL() bool {
	subtag := strings.ToLower(primarySubtag(string(l)))
	if subtag == "" {
		return false
	}
	if _, ok := rtlPrimarySubtags[subtag]; ok {
		return true
	}
	return false
}

// InvalidLocaleError signals that a locale tag is not supported by the loaded bundle.
type InvalidLocaleError struct{ Tag string }

func (e *InvalidLocaleError) Error() string {
	if e.Tag == "" {
		return "i18n: invalid locale"
	}
	return fmt.Sprintf("i18n: invalid locale %q", e.Tag)
}

// IsInvalidLocaleError reports whether err's tree contains an *InvalidLocaleError.
func IsInvalidLocaleError(err error) bool {
	var target *InvalidLocaleError
	return errors.As(err, &target)
}

// rtlPrimarySubtags enumerates the BCP-47 primary language subtags rendered right-to-left.
// NOTE: overridden per-file via `dir: rtl` in the locale YAML front-matter.
var rtlPrimarySubtags = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"ar": {},
	"he": {},
	"fa": {},
	"ur": {},
	"ps": {},
	"sd": {},
	"yi": {},
}

func primarySubtag(tag string) string {
	if tag == "" {
		return ""
	}
	if i := strings.IndexByte(tag, '-'); i > 0 {
		return tag[:i]
	}
	if i := strings.IndexByte(tag, '_'); i > 0 {
		return tag[:i]
	}
	return tag
}

func normalizeTag(tag string) string {
	if tag == "" {
		return ""
	}
	tag = strings.TrimSpace(tag)
	tag = strings.ReplaceAll(tag, "_", "-")
	return tag
}

func matchTag(tag string, supported []Locale) (Locale, bool) {
	if tag == "" || len(supported) == 0 {
		return "", false
	}
	norm := normalizeTag(tag)
	for _, l := range supported {
		if strings.EqualFold(string(l), norm) {
			return l, true
		}
	}
	prim := primarySubtag(norm)
	for _, l := range supported {
		if strings.EqualFold(primarySubtag(string(l)), prim) {
			return l, true
		}
	}
	return "", false
}

func parseAcceptLanguage(header string) []string {
	if header == "" {
		return nil
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.String())
	}
	return out
}
