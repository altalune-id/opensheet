package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	themeTokenVar   = regexp.MustCompile(`--([a-z0-9-]+):\s*[\d.]+ [\d.]+% [\d.]+%;`)
	classTokenSplit = regexp.MustCompile("[\\s\"'`{}(),]+")
)

const tokenUtilityPrefixes = `text|bg|border(?:-[trblxyse])?|ring(?:-offset)?|outline|divide|fill|stroke|from|via|to|accent|caret|decoration|shadow|placeholder`

// TestThemeTokenUtilitiesAreDefined fails when a template uses a theme-token utility that no stylesheet defines.
func TestThemeTokenUtilitiesAreDefined(t *testing.T) {
	var css strings.Builder
	for _, name := range []string{"static/app.css", "static/themes.css", "static/basecoat.css"} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		css.Write(b)
	}
	themes, err := staticFS.ReadFile("static/themes.css")
	if err != nil {
		t.Fatalf("read themes.css: %v", err)
	}
	utility := tokenUtilityPattern(t, string(themes))

	used := map[string][]string{}
	for _, src := range classSources(t) {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		for _, class := range classTokenSplit.Split(string(b), -1) {
			base := strings.TrimLeft(class[strings.LastIndex(class, ":")+1:], "!-")
			if utility.MatchString(base) {
				used[class] = append(used[class], filepath.Base(src))
			}
		}
	}
	if len(used) == 0 {
		t.Fatal("found no theme-token utilities in the templates; the scan is broken")
	}

	sheet := css.String()
	var missing []string
	for class, files := range used {
		if !selectorDefined(sheet, "."+escapeClass(class)) {
			missing = append(missing, class+" <- "+strings.Join(dedupe(files), ", "))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d theme-token utilities are used but defined in no stylesheet — check internal/web/tailwind.config.js and run `make ui-vendor`:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

func tokenUtilityPattern(t *testing.T, themes string) *regexp.Regexp {
	t.Helper()
	seen := map[string]bool{}
	var tokens []string
	for _, m := range themeTokenVar.FindAllStringSubmatch(themes, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			tokens = append(tokens, regexp.QuoteMeta(m[1]))
		}
	}
	if len(tokens) == 0 {
		t.Fatal("found no HSL theme variables in themes.css")
	}
	return regexp.MustCompile(`^(?:` + tokenUtilityPrefixes + `)-(?:` + strings.Join(tokens, "|") + `)(?:/\d{1,3})?$`)
}

func classSources(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".templ") {
			out = append(out, path)
			return nil
		}
		if filepath.Dir(path) == "static" && strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".min.js") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	return out
}

func escapeClass(class string) string {
	var b strings.Builder
	for i := 0; i < len(class); i++ {
		if !isClassByte(class[i]) || class[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(class[i])
	}
	return b.String()
}

func selectorDefined(sheet, selector string) bool {
	for i := 0; ; {
		j := strings.Index(sheet[i:], selector)
		if j < 0 {
			return false
		}
		end := i + j + len(selector)
		if end >= len(sheet) || !isClassByte(sheet[end]) {
			return true
		}
		i = end
	}
}

func isClassByte(c byte) bool {
	return c == '-' || c == '_' || c == '\\' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func dedupe(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
