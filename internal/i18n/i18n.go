// Package i18n resolves per-request locale and translates message keys.
package i18n

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

//go:embed locales/active.*.yaml
var localesFS embed.FS

// LocaleFilenamePrefix is the prefix every embedded locale filename must carry.
const LocaleFilenamePrefix = "active."

// LocaleFilenameSuffix is the extension every embedded locale filename must carry.
const LocaleFilenameSuffix = ".yaml"

// Bundle is the loaded set of translations. Safe for concurrent use.
type Bundle struct {
	inner      *i18n.Bundle
	defaultLoc Locale
	locales    []Locale
	dirs       map[Locale]string
}

// EmbeddedLocalesFS returns the embedded fs.FS holding the built-in locale files.
func EmbeddedLocalesFS() fs.FS { return localesFS }

// NewEmbeddedBundle builds a Bundle from the embedded locales/ directory.
func NewEmbeddedBundle(defaultLoc Locale) *Bundle {
	return NewBundle(localesFS, defaultLoc)
}

// NewBundle loads active.*.yaml files from fsys into a Bundle. Panics if loading fails.
func NewBundle(fsys fs.FS, defaultLoc Locale) *Bundle {
	if defaultLoc == "" {
		defaultLoc = EnUS
	}
	tag, err := language.Parse(string(defaultLoc))
	if err != nil {
		panic(fmt.Errorf("i18n: parse default locale %q: %w", defaultLoc, err))
	}
	inner := i18n.NewBundle(tag)
	inner.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

	files, err := findLocaleFiles(fsys)
	if err != nil {
		panic(fmt.Errorf("i18n: scan locales: %w", err))
	}
	if len(files) == 0 {
		panic(fmt.Errorf("i18n: no locale files matched %s*%s", LocaleFilenamePrefix, LocaleFilenameSuffix))
	}
	sort.Strings(files)

	dirs := make(map[Locale]string, len(files))
	locales := make([]Locale, 0, len(files))

	for _, name := range files {
		buf, rerr := fs.ReadFile(fsys, name)
		if rerr != nil {
			panic(fmt.Errorf("i18n: read %s: %w", name, rerr))
		}
		code, dir, body, perr := parseLocaleFile(name, buf)
		if perr != nil {
			panic(fmt.Errorf("i18n: parse %s: %w", name, perr))
		}
		clean, cerr := stripMetaBlock(body)
		if cerr != nil {
			panic(fmt.Errorf("i18n: strip meta %s: %w", name, cerr))
		}
		if _, lerr := inner.ParseMessageFileBytes(clean, name); lerr != nil {
			panic(fmt.Errorf("i18n: load %s: %w", name, lerr))
		}
		locales = append(locales, code)
		dirs[code] = dir
	}

	if _, ok := dirs[defaultLoc]; !ok {
		panic(fmt.Errorf("i18n: default locale %q not among loaded locales", defaultLoc))
	}

	b := &Bundle{inner: inner, defaultLoc: defaultLoc, locales: locales, dirs: dirs}
	return b
}

// Default returns the bundle's fallback locale.
func (b *Bundle) Default() Locale { return b.defaultLoc }

// All returns every locale loaded into the bundle.
func (b *Bundle) All() []Locale { return slices.Clone(b.locales) }

// Has reports whether the bundle carries messages for tag.
func (b *Bundle) Has(tag string) bool {
	_, ok := matchTag(tag, b.locales)
	return ok
}

// Parse returns the loaded Locale that best matches tag, or InvalidLocaleError.
func (b *Bundle) Parse(tag string) (Locale, error) {
	if loc, ok := matchTag(tag, b.locales); ok {
		return loc, nil
	}
	return b.defaultLoc, &InvalidLocaleError{Tag: tag}
}

// Dir returns "rtl" or "ltr" for the given loaded locale (file-declared override wins).
func (b *Bundle) Dir(l Locale) string {
	if d, ok := b.dirs[l]; ok && d != "" {
		return d
	}
	return l.Dir()
}

// Negotiate returns the best-matching Locale among preferred tags, falling back to fallback then to the bundle default.
func (b *Bundle) Negotiate(fallback Locale, preferred ...string) Locale {
	for _, tag := range preferred {
		if tag == "" {
			continue
		}
		if loc, ok := matchTag(tag, b.locales); ok {
			return loc
		}
	}
	if loc, ok := matchTag(string(fallback), b.locales); ok {
		return loc
	}
	return b.defaultLoc
}

// For returns a Translator scoped to loc; unsupported tags fall back to the default.
func (b *Bundle) For(l Locale) *Translator {
	if _, ok := matchTag(string(l), b.locales); !ok {
		l = b.defaultLoc
	}
	loc := i18n.NewLocalizer(b.inner, string(l), string(b.defaultLoc))
	def := loc
	if l != b.defaultLoc {
		def = i18n.NewLocalizer(b.inner, string(b.defaultLoc))
	}
	return &Translator{loc: loc, def: def, locale: l}
}

// Translator resolves keys for a single request's locale. Not safe for concurrent use.
type Translator struct {
	loc    *i18n.Localizer
	def    *i18n.Localizer
	locale Locale
}

// Locale returns the locale this Translator was constructed for.
func (t *Translator) Locale() Locale { return t.locale }

// T returns the translation for key. Optional args are key/value pairs.
func (t *Translator) T(key string, args ...any) string {
	if t == nil || t.loc == nil {
		return key
	}
	cfg := &i18n.LocalizeConfig{MessageID: key}
	if data, ok := templateData(args); ok {
		cfg.TemplateData = data
	}
	if out, err := t.loc.Localize(cfg); err == nil && out != "" {
		return out
	}
	if t.def != nil && t.def != t.loc {
		if out, err := t.def.Localize(cfg); err == nil && out != "" {
			return out
		}
	}
	return key
}

// Tn returns the pluralized translation for key. Count is auto-injected; extra args are key/value pairs.
func (t *Translator) Tn(key string, n int, args ...any) string {
	if t == nil || t.loc == nil {
		return key
	}
	data, _ := templateData(args)
	if data == nil {
		data = map[string]any{}
	}
	data["Count"] = n
	cfg := &i18n.LocalizeConfig{
		MessageID:    key,
		PluralCount:  n,
		TemplateData: data,
	}
	if out, err := t.loc.Localize(cfg); err == nil && out != "" {
		return out
	}
	if t.def != nil && t.def != t.loc {
		if out, err := t.def.Localize(cfg); err == nil && out != "" {
			return out
		}
	}
	return key
}

func templateData(args []any) (map[string]any, bool) {
	if len(args) == 0 {
		return nil, false
	}
	m := make(map[string]any, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		m[key] = args[i+1]
	}
	if len(m) == 0 {
		return nil, false
	}
	return m, true
}

func findLocaleFiles(fsys fs.FS) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := path.Base(p)
		if !strings.HasPrefix(name, LocaleFilenamePrefix) || !strings.HasSuffix(name, LocaleFilenameSuffix) {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type localeFileMeta struct {
	Meta struct {
		Dir string `yaml:"dir"`
	} `yaml:"_meta"`
}

func parseLocaleFile(filename string, buf []byte) (loc Locale, dir string, body []byte, err error) { //nolint:nonamedreturns // named returns document the four distinct outputs
	base := path.Base(filename)
	stem := strings.TrimSuffix(strings.TrimPrefix(base, LocaleFilenamePrefix), LocaleFilenameSuffix)
	if stem == "" {
		return "", "", nil, fmt.Errorf("empty locale code in filename")
	}
	if !isBCP47Shape(stem) {
		return "", "", nil, fmt.Errorf("invalid BCP-47 shape: %q", stem)
	}
	loc = Locale(stem)
	switch {
	case headerDirComment(buf) != "":
		dir = headerDirComment(buf)
	case parseMetaBlock(buf) != "":
		dir = parseMetaBlock(buf)
	default:
		dir = loc.Dir()
	}
	return loc, dir, buf, nil
}

func headerDirComment(buf []byte) string {
	for _, line := range strings.SplitN(string(buf), "\n", 20) {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if !strings.HasPrefix(trim, "#") {
			return ""
		}
		body := strings.TrimSpace(strings.TrimLeft(trim, "#"))
		if rest, ok := strings.CutPrefix(body, "dir:"); ok {
			val := strings.ToLower(strings.TrimSpace(rest))
			if val == "rtl" || val == "ltr" {
				return val
			}
		}
		if rest, ok := strings.CutPrefix(body, "rtl:"); ok {
			val := strings.ToLower(strings.TrimSpace(rest))
			if val == "true" {
				return "rtl"
			}
			if val == "false" {
				return "ltr"
			}
		}
	}
	return ""
}

func parseMetaBlock(buf []byte) string {
	var m localeFileMeta
	if err := yaml.Unmarshal(buf, &m); err != nil {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(m.Meta.Dir))
	if v == "rtl" || v == "ltr" {
		return v
	}
	return ""
}

func isBCP47Shape(s string) bool {
	if s == "" {
		return false
	}
	for part := range strings.SplitSeq(s, "-") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
			if !ok {
				return false
			}
		}
	}
	return true
}

func stripMetaBlock(buf []byte) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(buf, &doc); err != nil {
		return buf, nil //nolint:nilerr // if not a map document, pass through unchanged
	}
	if _, ok := doc["_meta"]; !ok {
		return buf, nil
	}
	delete(doc, "_meta")
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return out, nil
}
