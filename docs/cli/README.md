# opensheet CLI contract

Surface **S6** ([`surfaces`](../surfaces/README.md)). The stable interface for scripting, automation and
agents. Semver applies from v1.0.0; pre-v1 any minor release may break it, so pin exact versions.

```
opensheet [global flags] <command> [subcommand] [args] [flags]
```

Built by Cobra factories in `internal/cli/`, rooted at `NewRootCmd`. The `--help` group headings
(Runtime, Auth, Tenancy, Domain, Meta) are cosmetic and not part of the contract. Adding a command
is a procedure, not a contract: [`howto/cli-command.md`](../howto/cli-command.md).

| Part                          | Covers                                                                                  |
| ----------------------------- | --------------------------------------------------------------------------------------- |
| [`commands`](commands.md)     | every command with its args, flags and printed contract; `todo`; `blog`; payload fields |
| [`resolution`](resolution.md) | global flags, token and URL precedence, the `healthz` carve-out                         |

## How a command reaches the domain

R11: the CLI owns no verbs. What a command calls decides which credential it needs.

| Reaches                                    | Commands                                                                    | Credential          |
| ------------------------------------------ | --------------------------------------------------------------------------- | ------------------- |
| in-process services (`ServerBootFn`)       | `init`, `serve`, `migrate`, `scheduler`, `auth`, `org`, `project`, `invite` | session file, or DB |
| control plane S2, Connect (`ClientBootFn`) | `todo`                                                                      | bearer token        |
| data plane S3, REST                        | `blog`                                                                      | API key             |
| nothing but `config.Load`                  | `version`, `healthz`, `completion`                                          | none                |

`org`, `project`, `invite` and `todo` resolve a principal before doing anything, so they fail with
`not signed in` without a session or `--token`.

## Command tree

Args, flags and what each command prints: [`commands`](commands.md).

```
opensheet
├─ Runtime   init · serve · migrate {up,status,down-to} · scheduler {list,run}
├─ Auth      auth {login,logout,whoami,token mint}
├─ Tenancy   org {list,create} · project {list,create} · invite {list,send,revoke}
├─ Domain    todo {list,add,toggle,delete}                             control plane S2
│            blog {list,get,create,update,publish,unpublish,delete}    data plane S3
└─ Meta      version · healthz · completion
```

Global flags, and how a token or a URL is resolved: [`resolution`](resolution.md).

## Exit codes

Source of truth: `internal/cli/exit.go`. `ExitCodeFor` maps the response's
`apperror.AppError.GRPCCode()`; anything that is not an `AppError` returns `1`.

| Code | gRPC code            | Meaning                                           |
| ---- | -------------------- | ------------------------------------------------- |
| `0`  | `OK`                 | success; also a cancelled or timed-out context    |
| `1`  | —                    | general error, and every non-`AppError` failure   |
| `2`  | `Unauthenticated`    | token invalid, expired, or rejected by the server |
| `3`  | `PermissionDenied`   | credential valid, wrong scope                     |
| `4`  | `InvalidArgument`    | validation error                                  |
| `5`  | `NotFound`           | not found                                         |
| `6`  | `AlreadyExists`      | conflict — already exists, or replayed key        |
| `7`  | `FailedPrecondition` | onboarding required, stale version, draining      |

`ExitUsage = 64` is declared but nothing returns it. A bad flag, a missing required flag or a
mutually-exclusive pair exits `1` like any other error.

Error code strings (`GEN005`, `TDO001`, …): [`error codes`](../errors/README.md).

## Output

`--output` picks the format; `render.Detect` falls back to `OPENSHEET_OUTPUT`, then to text on a TTY and
json otherwise. An unrecognised value falls back to text.

| Format   | Envelope                                                        |
| -------- | --------------------------------------------------------------- |
| `text`   | aligned table for lists, `key: value` lines for single records  |
| `json`   | `{"data": <shape>}` — indented, HTML escaping off, no `meta`    |
| `ndjson` | one compact JSON object per line, no envelope — **`blog` only** |

Every command except `blog` treats `ndjson` as `json` and emits the `data` envelope. Only
`renderPosts` / `renderPost` call `render.NDJSON`.

Per-command fields under `data`: [`commands`](commands.md#payload-fields).

### Errors

`cmd/opensheet/main.go` writes the failure through `slog` and returns the exit code. There is no
error envelope on stdout and `--output` does not affect it. One line on **stderr**:

```
2026/01/02 15:04:05 ERROR opensheet error="blog get: no such org, project or post"
```

`internal/cli/render/error.go` defines a structured alternative
(`{"error":{"code","message","exit"}}` in json, `error:` / `code:` / `exit:` in text) that nothing
calls yet. Scripts must key off the **exit code**, not stderr text.

## Stability guarantees (post-v1.0.0)

| Stable                                             | Not stable                        |
| -------------------------------------------------- | --------------------------------- |
| command and subcommand names                       | log output and `--log-format`     |
| flag names                                         | stderr error text                 |
| exit codes                                         | table column order in `text` mode |
| error `code` values (`internal/apperror/codes.go`) | —                                 |
| field names under `data`                           | —                                 |

## Declared but not wired

Present in `--help` and accepted on the command line, but no code reads them. Listed so a script
does not depend on them; each has a `BACKLOG.md` entry.

| Flag                                | Today                                                                 |
| ----------------------------------- | --------------------------------------------------------------------- |
| `--no-interactive`                  | never consulted; prompts still appear                                 |
| `--log-level`, `--log-format`       | flags ignored; `OPENSHEET_LOG_LEVEL` / `OPENSHEET_LOG_FORMAT` do work |
| `auth login --print-token`          | plumbed into `loginOpts`, never read; no token is printed             |
| `init --project-slug`               | echoed in the success line; no project is created                     |
| `--org`, `--project` outside `blog` | ignored; the session principal decides                                |

No command writes a session profile (`saveProfile` has no production caller), so `blog` in practice
needs an explicit `--token` / `OPENSHEET_TOKEN` plus `--org` and `--project`.

## Not on this surface

No CLI command for API keys, scopes or MCP. Keys are minted on the console (S1) and the control
plane (S2); the MCP surface is a server mount, not a subcommand — see [`mcp`](../mcp/README.md) and
[`scopes`](../scopes/README.md).
