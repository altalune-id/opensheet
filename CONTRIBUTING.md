# Contributing to opensheet

## Workflow

1. Write a failing test that names the behavior.
2. Make it pass with the minimum code.
3. Run `make check` (fmt + vet + templ-normalize + race tests).
4. Refactor with all tests green.

Coverage floor: aggregate ≥ 90%, service ≥ 85%. No CI job enforces the
number; reviewers do. Exported root packages (`httpclient/`,
`logger/`, `mailer/`, `mcp/`, `nanoid/`, `reqid/`, `scheduler/`,
`telemetry/`, `worker/`) get rich table-driven tests — they are copied
verbatim into forks, so signature changes here are expensive to propagate.

## Commits

- Reviewer commits, not authors. The executing agent (or human) prepares
  changes; the reviewer inspects `git status && git diff` before committing.
- Every commit MUST be GPG- or SSH-signed. `pre-push` runs `git verify-commit`
  over the outgoing range, but the durable gate is branch protection on `main`
  — `--no-verify` bypasses the hook, not the gate. The rest of what the hooks
  run is in [`.husky/README.md`](.husky/README.md).
- Wrap errors with context: `fmt.Errorf("boot: migrate: %w", err)`.
- Assert with `errors.Is` for sentinels, `errors.AsType[T]` for typed errors.

## Testing

```bash
make test               # unit (fast, no external deps)
make test-race          # unit + -race (also run by `make check`)
make test-cover         # unit + coverage summary
make test-integration   # integration (ephemeral PG via testcontainers, or TEST_PG_DSN)
make test-all           # both
```

Integration tests carry `//go:build integration`, so `go test ./...` never
touches them. Bare `make test-integration` spins ephemeral Postgres through
`pgtest.New(t)` and needs a docker or podman socket; set `TEST_PG_DSN` to
reuse a running cluster instead, which is faster:

```bash
TEST_PG_DSN='postgres://opensheet:opensheet@localhost:5432/opensheet_test?sslmode=disable' \
    make test-integration
```

Integration tests live beside their unit counterparts
(`postgres_integration_test.go` next to `postgres.go`); the full per-module
file set is [`modules`](docs/modules/README.md).

## Releasing

Two workflows drive publication:

- `.github/workflows/dev.yml` — every push to `main` builds a multi-arch
  Docker image and pushes `:edge` + `:<short-sha>` to GHCR.
- `.github/workflows/release.yml` — a tag matching `v*.*.*` (or a
  published GitHub Release, or `workflow_dispatch`) runs GoReleaser:
  cross-compiled binaries (linux/darwin × amd64/arm64), tar.gz archives,
  checksums, SBOMs, cosign signatures, Docker `:{version}` + `:latest`.

Cut a release with `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`.
Pre-releases: `v0.2.0-rc.1` — GoReleaser's `prerelease: auto` marks them.
Never re-tag or delete a published version — the module proxy is forever.
Roll a new version with a `retract` directive in `go.mod` instead.

## Adding a domain module

A new bounded context under `internal/<name>/`. The file set, tenant scoping,
migrations, error codes and boot wiring are all in
[`modules`](docs/modules/README.md) — follow its checklist.
Write the failing `service_test.go` against the fake before the store exists;
the template doc describes the shape, not the order.

## Adding a platform primitive

A cross-cutting capability, in a root package or under `internal/platform/`.
Shape, import boundary and Kernel wiring are in
[`platform`](docs/platform/README.md).
A root package is copied verbatim into every fork, so land its contract test
first — a signature change after the fact is expensive to propagate.

## Style

- `gofmt -w` before commit.
- One package per directory. No `pkg/` hierarchy.
- 1-line godoc on exported symbols, starting with the symbol name.
- No `TODO(name):` — link an issue.
- No mocks (`sqlmock`, `gomock`, `testify/mock`). Fakes are hand-written.

## Version policy

Pre-1.0: any minor version may break. Pin exact versions. v1.0.0 freezes
the exported surface and the CLI contract in
[`cli`](docs/cli/README.md) plus the error codes in
`internal/apperror/codes.go`.
