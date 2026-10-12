// Command comment-lint enforces the repository comment discipline over Go sources.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"altalune.id/opensheet/cmd/comment-lint/internal/comments"
)

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

func run(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("comment-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	list := fs.Bool("list", false, "print violations but exit 0")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := comments.Run(comments.Options{Roots: fs.Args()})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "comment-lint:", err)
		return 2
	}

	report.Print(stdout)

	if len(report.Violations) > 0 && !*list {
		_, _ = fmt.Fprintln(stderr, "comment-lint: check failed")
		return 1
	}
	return 0
}
