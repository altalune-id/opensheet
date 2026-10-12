# Convention pass

The contracts live in [`docs/`](../../../../docs/). Start with the failures that pass review —
they apply to every change. The module checklist applies when the diff touches a domain module
(`internal/<name>/`: aggregate, `Store`, migration, surface handler); skip rows that do not.
[`modules` Section 11](../../../../docs/modules/README.md#11-review-checklist) is its doc-side twin.

## Things that pass review while being wrong

These are the failures that survived a plan, a spec review, and a green test suite in this
codebase. Look for them specifically.

**Tests that cannot fail, fakes that enforce the thing under test, guards with no coverage,
route probes mistaken for feature tests** — the `altalune-go-convention` skill owns these, in
"Tests that cannot fail". Apply every one; both halves of the fake rule matter (a fake must not
filter by project, and a versioned `Store`'s fake _must_ honour `ifVersion`).

**A fixture that cannot prove what it claims.** The default integration fixture connects as the
container superuser, which bypasses RLS outright, so `..._OtherOrgIsInvisible` on it proves
nothing. A real one migrates as a BYPASSRLS owner with `RLSEnforce=true`, binds the store to a
separate `NOBYPASSRLS` login role, and asserts the policies exist.

**A conditional write split in two.** Both halves guard correctly, both tests pass, and two
writers still interleave between them. Ask what a second writer does _between_ the statements.

**A `Delete` that never loads the row.** It has no scope check at all — the store's org filter is
the only thing between a sibling project and a destructive write.

**An edited migration on a live schema.** Goose does not checksum, so `migrate up` silently does
nothing and the change appears to have been applied.

**A success message for work that did not happen.** Check that a reported side effect has a
corresponding write. A command printing `project=default` while creating no project is a real
example from this codebase.

**A comment describing a mechanism that cannot occur.** Worse than no comment, because it will be
trusted. Verify the claim before copying a rationale from a neighbouring file.

**A test that is green on macOS and red on Linux CI.** Postgres `timestamptz` is microsecond
precision; `time.Now()` on macOS is already microsecond-granular, so a nanosecond value survives
a round trip locally but not on Linux. `got.CreatedAt.Equal(want.CreatedAt)` therefore passes on
every developer machine and fails in CI. Compare against
`want.CreatedAt.Truncate(time.Microsecond)`, as `internal/org/definer_integration_test.go` does.
SQLite keeps the full nanosecond value through `SQLiteTime`, so the same aggregate round-trips at
different precision per driver.

## Module checklist

Check every rule in `altalune-go-convention` "Rules that are expensive to get wrong" and
[`modules` Section 11](../../../../docs/modules/README.md#11-review-checklist). Then these, which
neither lists:

- `Store` lives in `store.go` beside the aggregate. Verbs only, `context.Context` first. No
  `postgres_repo.go`, no splitting by layer.
- Every service method opens a span.
- Every SQLite timestamp goes through `SQLiteTime`, bind sites included.
- The version guard rejects anything outside `[1, math.MaxInt32]`.
- Migration has `ENABLE` + `FORCE` + a policy, inside `{{if .RLSEnforce}}`, with the
  `{{.TablePrefix}}` literal; `make tenant-tables` regenerated.
- New machine-surface procedures appear in their scope table and their verb registry.
- Every `<script>` carries `nonce={ d.Nonce }`, every htmx attribute `hx-nonce={ d.Nonce }`.
- Cross-module references by UUID only.
- No `log.Println`. No `fmt.Print*` in domain code.

## Gates

`altalune-go-convention/scripts/verify.sh --check` runs these read-only: gofmt, vet, race tests,
`make lint` (depguard, forbidigo), `config-examples-check`, `templ-normalize-check`,
`i18n-check`, `comment-check`, and the upsert, error-code, route and scope guard tests. The race
detector is not optional: the stores build jet expressions concurrently.

Not covered read-only — ask before running, since they write or boot:

| Gate                                 | Catches                                            |
| ------------------------------------ | -------------------------------------------------- |
| `make tenant-tables`, then diff      | a new tenant table missing from the generated list |
| `make generate`, then diff           | stale templ or buf output                          |
| `verify.sh --check --integration`    | anything touching `postgres.go` or a migration     |
| `bash scripts/verify-serve-smoke.sh` | boot, `/healthz`, clean SIGTERM shutdown           |
| `bash scripts/verify-mcp-smoke.sh`   | the MCP surface, an `(mcp.v1.tool)` annotation     |

## Verifying a claim

Prefer evidence over reasoning when both are available. `EXPLAIN` the query, run the mutation,
boot the binary, open the page. Several conclusions here were confidently wrong until someone ran
the thing — "the lint rule is inert" (it was not; the test used an allowed stdlib import) and
"the ORDER BY is load-bearing" (it was not; `SECURITY DEFINER` functions are never inlined).
