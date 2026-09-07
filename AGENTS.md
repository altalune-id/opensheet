# AGENTS.md

Guide for AI coding agents (Claude Code, Codex, Cursor, …) working in this
repo. Read this before touching code.

## What altempl is

Multitenant Go template. Templ + HTMX + Connect-RPC on one HTTP listener.
Downstream services fork this shape and swap the domain modules. Signatures
under `authl/`, `logger/`, `mailer/`, `nanoid/`, `reqid/`, `scheduler/`,
`telemetry/`, `worker/`, and `internal/platform/` are copied verbatim into
forks — signature changes cost every fork of churn, so land tests first.

## Read first

- [`README.md`](README.md) — layout, config, modes, docker, releasing.
- [`docs/MODULE_TEMPLATE.md`](docs/MODULE_TEMPLATE.md) — the 7-file shape
  every domain module follows. Reference impl: `internal/todo/`.
- [`docs/PLATFORM_TEMPLATE.md`](docs/PLATFORM_TEMPLATE.md) — how to add
  cross-cutting primitives. Reference impls: `internal/platform/session/`,
  `internal/platform/tokens/`, `worker/`.
- [`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md) — stable command tree,
  exit codes, output envelopes.
- [`docs/MULTITENANCY_CONTEXT.md`](docs/MULTITENANCY_CONTEXT.md) — how a request
  gets its tenant scope. Read before adding or moving any tenant-scoped route.
- [`GLOSSARY.md`](GLOSSARY.md) — canonical terminology. Check a term here
  before inventing a synonym.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — TDD, commits, signing.

## Skills

Skill files under `.claude/skills/` set the review bar for this repo.
When invoked (Claude Code loads them automatically on task match):

- `go` — idiomatic Go (spf13), current through Go 1.25/1.26.
- `cobra-viper` — CLI + config conventions.
- `go-release` — semver, breaking changes, tagging, GoReleaser.
- `go-spec-reviewer` — spec review before implementation.

## Rules that override defaults

- **Never `git commit` while implementing a plan.** The user reviews the
  full working tree at the end.
- **Never `--no-verify` or `--no-gpg-sign`.** Fix the hook failure instead.
- **Never `--section` (§) symbol in output.** Use "#" or the word "Section".
- **Errors are typed structs**, one per failure mode. Helper name is
  `Is<TypeName>` with the FULL type name (e.g. `IsNotFoundError`), never
  `IsErr*`, never a shortcut.
- **No mocks.** Fakes are hand-written under `internal/testutil/fakes/`.
- **Comments** — default NO comments. Keep 1-line godoc on exported
  symbols, TODO/SECURITY/FIXME/NOTE markers, external URL references.
  Delete rationale, history, architecture prose.
- **Config awareness tags.** Every `Config` field carries an
  `awareness:"..."` struct tag (`required` / `bootstrap` / `secret` /
  `mode:<x>`). Cloud mode ALLOWS `Genesis.Email` for first-admin bootstrap.
- **No `else` on the happy path.** Return early.
- **Cobra commands** are built by factories (`NewRootCmd`), not package-level
  vars. Each command's business logic lives outside `cmd/` and knows
  nothing about Cobra or Viper.

## Common commands

```bash
make check              # fmt + vet + test — pre-commit gate
make test               # unit (fast)
make test-integration   # requires TEST_PG_DSN or docker/podman socket
make generate           # regenerate templ + buf outputs
make config-examples    # regenerate .env.example + config.example.yaml
make tenant-tables      # regenerate schema/tenant_tables_gen.go
make lint               # golangci-lint (or go vet fallback)
```

## Before finishing a task

- `make check` passes.
- Ran the relevant integration tests if you touched `postgres.go` or a
  migration.
- Regenerated `.env.example` / `config.example.yaml` if config struct
  tags changed.
- Regenerated `gen/` if you edited a `.proto`.
- Regenerated `tenant_tables_gen.go` if you added a tenant-scoped table.

## Verifying UI / server changes

Server smoke: `bash scripts/verify-serve-smoke.sh` boots `altempl serve`
on ephemeral SQLite + random port, curls `/healthz`, sends SIGTERM,
asserts clean shutdown within 10s.

Live probe against a running altempl: `altempl healthz` (auto-uses the
configured `http.addr`, or pass `--url http://host:port/healthz`).
Same binary is used as the compose/k8s healthcheck.

UI changes: `make dev` then browse. Type-checking passes ≠ feature works —
open the page.
