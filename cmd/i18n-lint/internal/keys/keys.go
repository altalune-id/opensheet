// Package keys scans templates and locale files and reports the diff.
package keys

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options carry the caller's request.
type Options struct {
	TemplatesDir string
	LocalesDir   string
	Fix          bool
}

// Usage records where a callsite lives.
type Usage struct {
	Path   string
	Line   int
	Key    string
	Plural bool
}

// LocaleFile is a parsed locale YAML with its message tree.
type LocaleFile struct {
	Path    string
	Locale  string
	Values  map[string]any
	Missing []string
}

// Report bundles the outcome of a scan.
type Report struct {
	Templates         []string
	Locales           []*LocaleFile
	Usages            []Usage
	Used              map[string]bool
	Plurals           map[string]bool
	Missing           map[string][]string
	Dead              []string
	IncompletePlurals map[string][]string
}

// Run scans templates and locales and returns a Report.
func Run(opts Options) (*Report, error) {
	usages, err := scanTemplates(opts.TemplatesDir)
	if err != nil {
		return nil, fmt.Errorf("scan templates: %w", err)
	}
	locales, err := loadLocales(opts.LocalesDir)
	if err != nil {
		return nil, fmt.Errorf("load locales: %w", err)
	}

	used := make(map[string]bool, len(usages))
	pluralKeys := make(map[string]bool)
	for _, u := range usages {
		used[u.Key] = true
		if u.Plural {
			pluralKeys[u.Key] = true
		}
	}

	missing := make(map[string][]string)
	incomplete := make(map[string][]string)
	for _, l := range locales {
		for k := range used {
			if !containsKey(l.Values, k) {
				missing[l.Locale] = append(missing[l.Locale], k)
			}
		}
		sort.Strings(missing[l.Locale])
		for k := range pluralKeys {
			if v := lookupKey(l.Values, k); v != nil {
				if !hasRequiredPluralForms(l.Locale, v) {
					incomplete[l.Locale] = append(incomplete[l.Locale], k)
				}
			}
		}
		sort.Strings(incomplete[l.Locale])
	}

	dead := deadKeys(locales, used)

	if opts.Fix {
		if err := applyFix(locales, missing); err != nil {
			return nil, fmt.Errorf("fix: %w", err)
		}
	}

	templatesList := uniquePaths(usages)
	sort.Strings(templatesList)

	return &Report{
		Templates:         templatesList,
		Locales:           locales,
		Usages:            usages,
		Used:              used,
		Plurals:           pluralKeys,
		Missing:           missing,
		Dead:              dead,
		IncompletePlurals: incomplete,
	}, nil
}

// Print writes a human-readable summary to w.
func (r *Report) Print(w io.Writer) {
	_, _ = fmt.Fprintf(w, "templates: %d files, %d callsites, %d unique keys\n", len(r.Templates), len(r.Usages), len(r.Used))
	_, _ = fmt.Fprintf(w, "locales:   %d files\n", len(r.Locales))
	if len(r.Missing) > 0 {
		_, _ = fmt.Fprintln(w, "\nmissing keys per locale:")
		locs := sortedKeys(r.Missing)
		for _, l := range locs {
			if len(r.Missing[l]) == 0 {
				continue
			}
			_, _ = fmt.Fprintf(w, "  %s:\n", l)
			for _, k := range r.Missing[l] {
				_, _ = fmt.Fprintf(w, "    - %s\n", k)
			}
		}
	}
	if len(r.IncompletePlurals) > 0 {
		_, _ = fmt.Fprintln(w, "\nincomplete plural forms per locale:")
		locs := sortedKeys(r.IncompletePlurals)
		for _, l := range locs {
			if len(r.IncompletePlurals[l]) == 0 {
				continue
			}
			_, _ = fmt.Fprintf(w, "  %s:\n", l)
			for _, k := range r.IncompletePlurals[l] {
				_, _ = fmt.Fprintf(w, "    - %s\n", k)
			}
		}
	}
	if len(r.Dead) > 0 {
		_, _ = fmt.Fprintln(w, "\ndead keys (present in some locale but never used in templates):")
		for _, k := range r.Dead {
			_, _ = fmt.Fprintf(w, "  - %s\n", k)
		}
	}
	if len(r.Missing) == 0 && len(r.IncompletePlurals) == 0 && len(r.Dead) == 0 {
		_, _ = fmt.Fprintln(w, "\nOK — every callsite has a translation in every loaded locale.")
	}
}

// HasBlockingIssues reports whether the report contains anything that should fail --check.
func (r *Report) HasBlockingIssues(strict bool) bool {
	for _, ks := range r.Missing {
		if len(ks) > 0 {
			return true
		}
	}
	for _, ks := range r.IncompletePlurals {
		if len(ks) > 0 {
			return true
		}
	}
	if strict && len(r.Dead) > 0 {
		return true
	}
	return false
}

var callsiteRe = regexp.MustCompile(`\bd\.(TrN?)\s*\(\s*"((?:[^"\\]|\\.)*)"`)

func scanTemplates(dir string) ([]Usage, error) {
	var usages []Usage
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return nil
		}
		buf, err := os.ReadFile(path) //nolint:gosec // G304: caller-supplied path
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(buf), "\n") {
			for _, m := range callsiteRe.FindAllStringSubmatch(line, -1) {
				usages = append(usages, Usage{
					Path:   path,
					Line:   i + 1,
					Key:    m[2],
					Plural: m[1] == "TrN",
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return usages, nil
}

func loadLocales(dir string) ([]*LocaleFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*LocaleFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "active.") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		locale := strings.TrimSuffix(strings.TrimPrefix(name, "active."), ".yaml")
		full := filepath.Join(dir, name)
		buf, err := os.ReadFile(full) //nolint:gosec // G304: caller-supplied path
		if err != nil {
			return nil, err
		}
		var values map[string]any
		if err := yaml.Unmarshal(buf, &values); err != nil {
			return nil, fmt.Errorf("parse %s: %w", full, err)
		}
		delete(values, "_meta")
		out = append(out, &LocaleFile{Path: full, Locale: locale, Values: values})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locale < out[j].Locale })
	return out, nil
}

func containsKey(values map[string]any, key string) bool {
	return lookupKey(values, key) != nil
}

func lookupKey(values map[string]any, key string) any {
	if v, ok := values[key]; ok {
		return v
	}
	segs := strings.Split(key, ".")
	var cur any = values
	for _, s := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m[s]
		if !ok {
			return nil
		}
		cur = v
	}
	return cur
}

func hasRequiredPluralForms(locale string, v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	required := requiredPluralForms(locale)
	for _, form := range required {
		if _, present := m[form]; !present {
			return false
		}
	}
	return true
}

func requiredPluralForms(locale string) []string {
	primary := locale
	if i := strings.IndexByte(locale, '-'); i > 0 {
		primary = locale[:i]
	}
	switch primary {
	case "ar":
		return []string{"zero", "one", "two", "few", "many", "other"}
	case "ru", "uk", "be", "hr", "cs", "pl", "sk":
		return []string{"one", "few", "many", "other"}
	case "ga":
		return []string{"one", "two", "few", "many", "other"}
	case "id", "ja", "ko", "th", "vi", "zh", "ms":
		return []string{"other"}
	default:
		return []string{"one", "other"}
	}
}

func deadKeys(locales []*LocaleFile, used map[string]bool) []string {
	seen := map[string]bool{}
	for _, l := range locales {
		collectLeafKeys(l.Values, "", seen)
	}
	var dead []string
	for k := range seen {
		if !used[k] {
			dead = append(dead, k)
		}
	}
	sort.Strings(dead)
	return dead
}

func collectLeafKeys(values map[string]any, prefix string, out map[string]bool) {
	for k, v := range values {
		if k == "_meta" {
			continue
		}
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch v := v.(type) {
		case map[string]any:
			if isPluralLeaf(v) {
				out[full] = true
			} else {
				collectLeafKeys(v, full, out)
			}
		default:
			out[full] = true
		}
	}
}

func isPluralLeaf(m map[string]any) bool {
	forms := []string{"zero", "one", "two", "few", "many", "other"}
	for _, f := range forms {
		if _, ok := m[f]; ok {
			return true
		}
	}
	return false
}

func applyFix(locales []*LocaleFile, missing map[string][]string) error {
	for _, l := range locales {
		add := missing[l.Locale]
		if len(add) == 0 {
			continue
		}
		if l.Values == nil {
			l.Values = map[string]any{}
		}
		for _, k := range add {
			l.Values[k] = ""
		}
		buf, err := yaml.Marshal(l.Values)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", l.Path, err)
		}
		if err := os.WriteFile(l.Path, buf, 0o644); err != nil { //nolint:gosec // G306: locale files are source
			return fmt.Errorf("write %s: %w", l.Path, err)
		}
	}
	return nil
}

func uniquePaths(usages []Usage) []string {
	seen := map[string]bool{}
	for _, u := range usages {
		seen[u.Path] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Contains reports whether s contains any element in list (test helper).
func Contains(list []string, s string) bool { return slices.Contains(list, s) }
