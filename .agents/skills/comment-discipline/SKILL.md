---
name: comment-discipline
description: Use when writing, reviewing or trimming code comments in any language — godoc or docstrings, verbose or rationale comments, section headers, a failing comment lint, or a request to clean up commenting in source, SQL, YAML, Makefile or Dockerfile files.
license: Proprietary
---

# Comment discipline

**Default: no comment.** The code and its names are the source of truth. Why a change was made
belongs in the commit message or PR, where it is dated and reviewed — not in the file.

The test before typing `//` or `#`: _would deleting this lose anything the code and its names
do not already say?_ If not, do not write it.

## Keep

| Keep                                   | Shape                                                                                             |
| -------------------------------------- | ------------------------------------------------------------------------------------------------- |
| doc on an exported / public symbol     | one sentence; in Go it starts with the identifier's name                                          |
| a marker                               | `TODO` `FIXME` `SECURITY` `NOTE` (also `XXX` `HACK` `BUG`)                                        |
| an external reference                  | a URL, `RFC 1234`, `GH-12`, `owner/repo#12`                                                       |
| a tool directive — not a comment       | `//go:build`, `//nolint`, `-- +goose Up`, `# syntax=`, `#!`, `# shellcheck` — no space after `//` |
| a config hint where the key is unclear | one line: `# 32 bytes, hex or base64`                                                             |

## Delete

- Rationale, trade-offs, design or architecture notes.
- History: "added after the X incident", "was Y before".
- Comments that restate the code: `// increment counter`.
- Doc comments on private / unexported symbols, unless every line is a marker or URL.
- Decorative section headers: `// --- repositories ---`, `# ===== db =====`.
- Blank `//` lines inside a doc block. Multi-line docs — keep one line. A marker that belongs
  with a doc joins the same line (see Examples); a second line fails a one-line-doc linter.

## Traps

- **A marker used as a loophole.** `// NOTE: we chose X because…` is still rationale. A
  `NOTE` or `SECURITY` line names a hazard the code does not show, in one line.
- **A wrong comment.** Worse than none, because it is trusted. Verify the claim before keeping
  or copying it from a neighbouring file.
- **Directives that look like comments.** Build tags, goose markers, shebangs and linter
  pragmas are read by tools. Removing one breaks the build or the migration.
- **Generated files.** Never trim them by hand; fix the generator or leave them.
- **`// nolint` with a space** is prose, not a directive — linters only read `//nolint`.
- **Deprecation.** Go reads a paragraph starting `Deprecated:`. Under a one-line-doc rule, make
  it the whole doc: `// Deprecated: use Bar.`

## Examples

```go
// Before
// checkOwner enforces ownership here so no transport can skip it.
// Reported as absent, not forbidden, so callers learn nothing.
func (s *Service) checkOwner(...)

// After
// SECURITY: reports absent, not forbidden, so callers learn nothing about other rows.
func (s *Service) checkOwner(...)
```

```go
// Before
// BySlug resolves the public identifier.
//
// SECURITY: callers must check the row's owner against their grant.
func (s *Service) BySlug(...)

// After
// BySlug resolves the public identifier. SECURITY: callers must check the row's owner.
func (s *Service) BySlug(...)
```

```go
// Before
// Load resolves configuration: overrides > env > file > default.
//
// The mode is validated at startup so a typo fails fast.
func Load(opts Options) (Config, error)

// After
// Load resolves configuration: overrides > env > file > default.
func Load(opts Options) (Config, error)
```

## Sweeping existing code

1. List violations with the repo's comment linter, or grep comment density per file and start
   from the densest.
2. Change comments only. Never touch code, signatures or directives.
3. Build and test after each package — a trimmed directive shows up there.
4. Report the violation or comment-line count before and after.

For a large sweep, give each package to a subagent with this file as its instructions.

## When not to apply

- Comments the user explicitly asks to keep.
- Tutorial or example code whose comments are the point.
- A public library whose godoc is its main documentation — still aim for one sentence.

## In opensheet

`make comment-check` (`cmd/comment-lint`) is the gate — **Go files only**; `make comment-list`
lists violations without failing. Rules: `multiline-doc` (any second content line, markers
included), `unexported-doc` (any prose line), `blank-doc-line`, `section-header`, and
`prose-block` (a free comment of 3+ prose lines with no marker or URL). `//i18n:use` counts as a
directive. It skips files headed `// Code generated … DO NOT EDIT.`, the `_templ.go` `.pb.go`
`.connect.go` `.mcp.go` `_gen.go` `.gen.go` suffixes, and `gen/ vendor/ testdata/ bin/ dist/
node_modules/ .github/`. Other file types follow this skill by review, not by the gate.
