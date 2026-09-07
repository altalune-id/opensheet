// Command i18n-lint verifies that every d.Tr / d.TrN callsite in .templ files
// is covered by every locale file under internal/i18n/locales/.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"altalune.id/template/cmd/i18n-lint/internal/keys"
)

func main() {
	code := run(os.Stdout, os.Stderr, os.Args[1:])
	os.Exit(code)
}

func run(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("i18n-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	templatesDir := fs.String("templates", "internal/web/templates", "directory scanned for .templ files")
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
