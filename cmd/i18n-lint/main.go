// Command i18n-lint verifies every Tr/TrN callsite in .templ and .go files has a translation in every locale.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"altalune.id/opensheet/cmd/i18n-lint/internal/keys"
)

func main() {
	code := run(os.Stdout, os.Stderr, os.Args[1:])
	os.Exit(code)
}

func run(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("i18n-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	templatesDir := fs.String("templates", "internal/web/templates", "directory scanned for .templ files")
	sources := fs.String("sources", ".", "comma-separated directories scanned for .go callsites and //i18n:use claims; empty disables")
	localesDir := fs.String("locales", "internal/i18n/locales", "directory holding active.*.yaml files")
	check := fs.Bool("check", false, "exit non-zero when keys are missing or plurals are incomplete")
	fix := fs.Bool("fix", false, "write empty placeholders for missing keys into each locale file")
	strict := fs.Bool("strict", false, "also fail on dead keys not referenced by any template")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := keys.Run(keys.Options{
		TemplatesDir: *templatesDir,
		LocalesDir:   *localesDir,
		SourceDirs:   splitDirs(*sources),
		Fix:          *fix,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "i18n-lint:", err)
		return 2
	}

	report.Print(stdout)

	if *check && report.HasBlockingIssues(*strict) {
		_, _ = fmt.Fprintln(stderr, "i18n-lint: check failed")
		return 1
	}
	return 0
}

func splitDirs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
