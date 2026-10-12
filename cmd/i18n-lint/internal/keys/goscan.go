package keys

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// NOTE: handlers call the Translator's T/Tn; templates call LayoutData's Tr/TrN.
var goCallsiteRe = regexp.MustCompile(`\b\w+\.(TrN|Tr|Tn|T)\s*\(\s*"((?:[^"\\]|\\.)*)"`)

// NOTE: anchored to the whole line, so prose merely mentioning the directive is not parsed as one.
var claimRe = regexp.MustCompile(`^\s*//\s*i18n:use\s+([A-Za-z0-9_.*-]+)\s*$`)

//nolint:gochecknoglobals // Immutable manifest; not runtime state.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"testdata":     true,
	"vendor":       true,
	"gen":          true,
	"bin":          true,
	"dist":         true,
}

func scanGo(dirs []string) ([]Usage, []Claim, error) {
	var usages []Usage
	var claims []Claim

	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") ||
				strings.HasSuffix(name, "_test.go") ||
				strings.HasSuffix(name, "_templ.go") {
				return nil
			}
			buf, err := os.ReadFile(path) //nolint:gosec // G304: caller-supplied path
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(buf), "\n") {
				if m := claimRe.FindStringSubmatch(line); m != nil {
					value := m[1]
					claims = append(claims, Claim{
						Path:   path,
						Line:   i + 1,
						Value:  strings.TrimSuffix(value, "*"),
						Prefix: strings.HasSuffix(value, "*"),
					})
					continue
				}
				for _, m := range goCallsiteRe.FindAllStringSubmatch(line, -1) {
					usages = append(usages, Usage{
						Path:   path,
						Line:   i + 1,
						Key:    m[2],
						Plural: m[1] == "TrN" || m[1] == "Tn",
					})
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return usages, claims, nil
}
