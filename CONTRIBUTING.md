# Contributing to altempl

## Workflow

1. Write a failing test that names the behavior.
2. Make it pass with the minimum code.
3. Run `make check` (fmt + vet + test).
4. Refactor with all tests green.

Coverage floor: aggregate ≥ 90%, service ≥ 85%. Exported root packages
(`authl/`, `logger/`, `mailer/`, `nanoid/`, `reqid/`, `telemetry/`,
`worker/`) get rich table-driven tests — signature changes here are
expensive to propagate.

## Commits

- Reviewer commits, not authors. The executing agent (or human) prepares
  changes; the reviewer inspects `git status && git diff` before committing.
- Every commit MUST be GPG- or SSH-signed. Hooks in [`.husky/`](.husky/README.md) enforce the rest.
- Wrap errors with context: `fmt.Errorf("boot: migrate: %w", err)`.
- Assert with `errors.Is` for sentinels, `errors.AsType[T]` for typed errors.

## Testing

```bash
make test               # unit (fast, no external deps)
make test-race          # unit + -race
make test-cover         # unit + coverage summary
make test-integration   # integration (ephemeral PG via testcontainers, or TEST_PG_DSN)
make test-all           # both
```

Integration tests carry `//go:build integration` — `go test ./...` never
touches them. Two ways to run them:

```bash
# 1) Zero setup — pgtest.New(t) spins ephemeral Postgres via testcontainers.
#    Requires a docker OR podman socket.
make test-integration

# 2) Reuse a running Postgres (faster).
TEST_PG_DSN='postgres://altempl:altempl@localhost:5432/altempl_test?sslmode=disable' \
    make test-integration
```

File-naming convention — integration tests live alongside their unit
counterparts:

```
internal/org/
├── postgres.go
├── postgres_integration_test.go   # //go:build integration
├── sqlite.go
└── sqlite_test.go                 # unit — sqlite in-process
```

## Releasing

Two workflows drive publication:

- `.github/workflows/dev.yml` — every push to `main` builds a multi-arch
  Docker image and pushes `:edge` + `:<short-sha>` to GHCR.
- `.github/workflows/release.yml` — a tag matching `v*.*.*` (or a
  published GitHub Release, or `workflow_dispatch`) runs GoReleaser:
  cross-compiled binaries (linux/darwin × amd64/arm64), tar.gz archives,
  checksums, SBOMs, cosign signatures, Docker `:{version}` + `:latest`.

Cut a release:

```bash
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

Pre-releases: `v0.2.0-rc.1` — GoReleaser's `prerelease: auto` marks them.
Never re-tag or delete a published version — the module proxy is forever.
Roll a new version with a `retract` directive in `go.mod` instead.

## Adding a domain module

See [`docs/MODULE_TEMPLATE.md`](docs/MODULE_TEMPLATE.md). Copy
`internal/todo/` as the reference. Checklist:

1. `internal/<name>/` with the canonical file set.
2. Fake in `internal/testutil/fakes/<name>.go`.
3. One field on `Server`, one constructor line in `internal/boot/server.go`.
4. Migrations under `schema/migrations/{postgres,sqlite}/`.
5. Add the table to `TenantTableSuffixes` (`make tenant-tables`) if tenant-scoped.
6. Register error codes in `internal/apperror/codes.go`.
7. API surface? Add `api/<name>/v1/*.proto` and `make generate`.

## Adding a platform primitive

See [`docs/PLATFORM_TEMPLATE.md`](docs/PLATFORM_TEMPLATE.md). Reference
implementations: `internal/platform/session/`, `internal/platform/tokens/`,
root-level `worker/`.

## Style

- `gofmt -w` before commit.
- One package per directory. No `pkg/` hierarchy.
- 1-line godoc on exported symbols, starting with the symbol name.
- No `TODO(name):` — link an issue.
- No mocks (`sqlmock`, `gomock`, `testify/mock`). Fakes are hand-written.

## Version policy

Pre-1.0: any minor version may break. Pin exact versions. v1.0.0 freezes
the exported surface and the CLI contract in
[`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md) plus the error codes in
`internal/apperror/codes.go`.
