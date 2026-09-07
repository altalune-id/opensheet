# altempl CLI contract

Stable interface for scripting, MCP server integration, and agent-driven usage.
Semver applies to this document from v1.0.0 onward. Pre-v1, breaking changes
are documented in the changelog.

## Command tree

The root binary is `altempl`. Commands are grouped by role (Runtime, Auth,
Tenancy, Domain, Meta) — the group label is cosmetic (it steers `--help`
output) and is not part of the contract.

```
altempl [global flags] <command> [subcommand] [args] [flags]
```

### Runtime

- `altempl serve`

  - Runs the HTTP server (web UI + Connect API + workers) on the configured
    `http.addr`. Blocks until the process is signalled.
  - Flags:
    - `--no-scheduler` — start everything except the periodic-job runner.
    - `--scheduler-only` — run only the periodic-job runner plus a
      health-endpoint listener. No web UI, no API.
  - The two are mutually exclusive; passing both is a usage error (`64`).
  - Everything else comes from `-c`/`--config` and the `ALT_*` env vars.

- `altempl scheduler list`

  - Lists every registered job. Reads the wired runner; does not run anything.
  - JSON shape (`--output=json`):
    ```json
    {
      "data": [
        {
          "name": "todo-autocomplete-stale",
          "scope": "tenant",
          "schedule": "cron \"0 */6 * * *\" UTC",
          "timeout": "2m0s",
          "singleton": true
        }
      ]
    }
    ```
  - Text output is a table: `NAME SCOPE SCHEDULE TIMEOUT SINGLETON`.

- `altempl scheduler run <job>`

  - Runs one job immediately, bypassing its schedule. Exactly one
    positional argument — the job `name` from `scheduler list`.
  - A `tenant`-scoped job fans out over every tenant, same as a scheduled tick.
  - A `singleton` job still takes the cross-process lock first.
  - Exit codes:
    - `5` — unknown job.
    - `6` — the job is already running here, or another replica holds the lock.
    - `7` — the runner is draining, or the scheduler is disabled
      (`scheduler.enabled=false`).

- `altempl migrate up`

  - Applies every pending migration end-to-end.

- `altempl migrate status`

  - Prints one row per migration: `<version> <applied-at | "pending"> <source>`.

- `altempl migrate down-to <version>`
  - Rolls the schema back to the given goose version (integer). Exactly one
    positional argument required.

### Auth

- `altempl auth login`

  - Signs in. Defaults to OIDC loopback (RFC 8252) when `oidc.issuer` +
    `oidc.clientID` are configured; falls back to a local prompt otherwise.
  - Flags:
    - `--admin` — break-glass: force local genesis login even when OIDC is
      configured. Prints a warning to stderr.
    - `--print-token` — print the bearer token on success (for scripts).
    - `--email <addr>` — email for local login (skips the interactive
      prompt).
    - `--password-stdin` — read the password from stdin (unmasked). Pairs
      with `--email`.

- `altempl auth logout`

  - Clears the local session file at `session.path`.

- `altempl auth whoami`

  - Prints the current signed-in principal.
  - JSON shape (`--output=json`):
    ```json
    {
      "data": {
        "user_id": "...",
        "email": "...",
        "name": "...",
        "source": "local|oidc",
        "session_path": "..."
      }
    }
    ```
  - Text shape (default on TTY):
    ```
    user_id: <uuid>
    email:   <addr>
    name:    <name>
    source:  <local|oidc>
    session: <path>
    ```

- `altempl auth token mint`
  - Mints a short-lived API token for the signed-in user.
  - Flags:
    - `--ttl <duration>` — token time-to-live (default `15m`). Accepts any
      `time.ParseDuration` value (e.g. `15m`, `1h`, `24h`).
  - NOTE: Currently a stub — blocked on the tokens issuer landing (spec
    Task 33). Returns a non-zero exit until then.

### Tenancy

- `altempl org list`

  - Lists orgs the caller is a member of.
  - JSON shape (`--output=json`):
    ```json
    {
      "data": [
        {
          "id": "...",
          "slug": "...",
          "name": "...",
          "owner_id": "...",
          "created_at": "..."
        }
      ]
    }
    ```

- `altempl org create --slug <slug> --name <name>`

  - Creates a new org. Both flags required.
  - Prints `Created org <slug> (<uuid>)` on success.

- `altempl project list`

  - Lists projects in the active org.
  - JSON shape (`--output=json`):
    ```json
    {
      "data": [
        {
          "id": "...",
          "org_id": "...",
          "slug": "...",
          "name": "...",
          "created_at": "..."
        }
      ]
    }
    ```

- `altempl project create --slug <slug> --name <name>`

  - Creates a new project in the active org. Both flags required.
  - Prints `Created project <slug> (<uuid>)` on success.

- `altempl invite list`

  - Lists pending invites for the active org.
  - JSON shape (`--output=json`):
    ```json
    {
      "data": [
        {
          "id": "...",
          "org_id": "...",
          "email": "...",
          "role": "owner|admin|member",
          "status": "pending|accepted",
          "expires_at": "...",
          "created_at": "..."
        }
      ]
    }
    ```

- `altempl invite send --email <addr> [--role member|admin|owner]`

  - Sends an invite to a new member of the active org.
  - `--email` required. `--role` defaults to `member`.

- `altempl invite revoke <id>`
  - Revokes a pending invite by its UUID.

### Domain

- `altempl todo list [--done | --open]`

  - Lists todos in the active project. `--done` and `--open` are mutually
    exclusive; omit both to list everything.
  - JSON shape (`--output=json`):
    ```json
    {
      "data": [
        {
          "id": "...",
          "project_id": "...",
          "title": "...",
          "done": false,
          "created_at": "..."
        }
      ]
    }
    ```

- `altempl todo add <title...>`

  - Creates a new todo. All positional arguments join with spaces to form
    the title.

- `altempl todo toggle <id>`

  - Flips the done flag on a todo. `<id>` must be a UUID.

- `altempl todo delete <id>`
  - Deletes a todo. `<id>` must be a UUID.

### Meta

- `altempl version`

  - Prints `altempl <version> (<commit>, built <buildTime>)`.
  - JSON shape (`--output=json`):
    ```json
    { "data": { "version": "...", "commit": "...", "buildTime": "..." } }
    ```

- `altempl healthz`

  - Probes the running server's `/healthz` endpoint. Exits `0` on 2xx,
    non-zero otherwise. Suitable for container healthchecks and k8s
    liveness/readiness probes — self-contained, no shell required.
  - `/healthz`, `/readyz`, and `/robots.txt` are mounted on the outer
    mux (root), NOT under `http.basePath`. This is intentional — probe
    URLs must not move when the app is remounted. The default URL never
    prepends `basePath`.
  - Flags:
    - `--url <url>` — override the target URL. Default derives from
      `http.addr`: `http://127.0.0.1:<port>/healthz`.
    - `--timeout <duration>` — request timeout (default `3s`).
  - JSON shape (`--output=json`):
    ```json
    {
      "data": {
        "url": "http://127.0.0.1:5150/healthz",
        "status": 200,
        "ok": true,
        "took": "3ms",
        "error": ""
      }
    }
    ```

- `altempl completion <bash|zsh|fish|powershell>`
  - Prints the completion script for the given shell to stdout. Source it
    from your shell rc.

## Global flags (persistent on root)

| Flag               | Env              | Default                          | Purpose                                            |
| ------------------ | ---------------- | -------------------------------- | -------------------------------------------------- |
| `-c, --config`     | `ALT_CONFIG`     | —                                | Config file (yaml).                                |
| `--token`          | `ALT_TOKEN`      | —                                | Bearer token (opaque).                             |
| `--token-file`     | `ALT_TOKEN_FILE` | —                                | Path to file containing bearer token (0600 mode).  |
| `--output`         | `ALT_OUTPUT`     | auto (text on TTY, json off-TTY) | Output format: `text` \| `json` \| `ndjson`.       |
| `--org`            | `ALT_ORG`        | —                                | Override active org (slug).                        |
| `--project`        | `ALT_PROJECT`    | —                                | Override active project (slug).                    |
| `--no-interactive` | —                | `false`                          | Never prompt; fail if a prompt would be needed.    |
| `--log-level`      | `ALT_LOG_LEVEL`  | `info`                           | Log level: `debug` \| `info` \| `warn` \| `error`. |
| `--log-format`     | `ALT_LOG_FORMAT` | `json`                           | Log format: `json` \| `text`.                      |

## Token precedence

`--token` > `ALT_TOKEN` > `--token-file` > `ALT_TOKEN_FILE` > `~/.altempl/session.json` > interactive login.

An invalid `--token` errors out (exit `2`). It does NOT fall back to
lower-precedence sources.

## Exit codes

| Code | Meaning                                                       |
| ---- | ------------------------------------------------------------- |
| 0    | success                                                       |
| 1    | general error                                                 |
| 2    | token invalid or expired                                      |
| 3    | forbidden (token valid, wrong scope)                          |
| 4    | validation error (bad input)                                  |
| 5    | not found                                                     |
| 6    | conflict (already exists)                                     |
| 7    | failed precondition (onboarding required, scheduler draining) |
| 64   | usage error (bad flag / missing argument)                     |

Exit codes are derived from the response's `apperror.AppError.GRPCCode()`
(see `internal/cli/exit.go` and `internal/apperror/codes.go`). Unknown
errors return `1`.

## Output envelopes

### Success — `--output=json`

Non-list responses:

```json
{"data": <shape>}
```

List responses (top-level envelope, meta carries pagination hints):

```json
{"data": [<item1>, <item2>, ...], "meta": {"count": N}}
```

Item shapes are defined per subcommand — see Command tree above.

### Success — `--output=ndjson`

One JSON object per line (streaming). For lists, each element is a line.
Non-list responses are a single line. No wrapping envelope.

### Errors — every format writes to stderr

JSON / NDJSON:

```json
{
  "error": {
    "code": "todo.not_found",
    "message": "Todo \"01H8...\" not found",
    "meta": { "todo_id": "01H8..." },
    "request_id": "V1St...",
    "trace_id": "4bf9..."
  }
}
```

Human / text mode (default on TTY):

```
Error: Todo "01H8..." not found (todo.not_found)
Request ID: V1St...
Trace ID:   4bf9...
```

## Stability guarantees (post-v1.0.0)

- Command names, subcommand names, flag names — stable.
- Exit codes — stable.
- Error `code` values — stable (see `internal/apperror/codes.go`).
- Output shapes under `data` — stable per subcommand (documented per-command
  above).
- Log format (`--log-format`) is NOT part of the contract — it may change
  between minor versions.

Pre-v1: subject to change in every minor release. Pin exact versions when
scripting against a pre-v1 CLI.
