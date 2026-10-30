# CLI commands

Every command, its args, flags, printed contract and payload fields. Surface and exit codes:
[`cli`](README.md). Global flags: [`resolution`](resolution.md).

## Commands

Group is the `--help` heading. `project` and `invite` act on the caller's active org, taken from
the session principal — **not** from `--org`.

| Group   | Command                                                 | Args           | Flags                                                                                                                                    | Contract / prints                                                                                                                    |
| ------- | ------------------------------------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Runtime | `init`                                                  | none           | `--email` (required), `--name` (defaults to `--email`), `--org-slug` (empty: generated), `--org-name`, `--project-slug`, `--interactive` | First-run bootstrap: creates the genesis admin and marks onboarding complete. Exits `6` when already onboarded (`ONB002`).           |
| Runtime | `serve`                                                 | none           | `--no-scheduler`, `--scheduler-only`, `--no-consumer`, `--consumer-only` — exclusions below                                              | Runs the HTTP listener on `http.addr` and blocks until signalled.                                                                    |
| Runtime | `migrate up`                                            | none           | —                                                                                                                                        | Applies every pending migration. Prints `migrations: up-to-date`.                                                                    |
| Runtime | `migrate status`                                        | none           | —                                                                                                                                        | One row per migration: `<version> <applied-at \| "pending"> <source>`.                                                               |
| Runtime | `migrate down-to <version>`                             | exactly 1, int | —                                                                                                                                        | Rolls the schema back to that goose version.                                                                                         |
| Runtime | `scheduler list`                                        | none           | —                                                                                                                                        | Lists registered jobs from the wired runner. Runs nothing.                                                                           |
| Runtime | `scheduler run <job>`                                   | exactly 1      | —                                                                                                                                        | Runs one job now, bypassing its schedule. Tenant-scoped jobs still fan out; singletons still take the cross-process lock.            |
| Auth    | `auth login`                                            | none           | `--admin`, `--print-token`, `--email`, `--password-stdin`                                                                                | Othra loopback sign-in (RFC 8252) on `oidc.cliClientID` when `oidc.issuer` is set; local genesis prompt otherwise.                   |
| Auth    | `auth logout`                                           | none           | —                                                                                                                                        | Removes the file at `session.path`.                                                                                                  |
| Auth    | `auth whoami`                                           | none           | —                                                                                                                                        | Prints the effective principal, one field per line: `user_id`, `email`, `name`, `source` (`local\|oidc\|session`), `session` (path). |
| Auth    | `auth token mint`                                       | none           | `--ttl` (default `15m`)                                                                                                                  | **Stub.** Always fails (exit `1`): it needs a token issuer.                                                                          |
| Tenancy | `org list`                                              | none           | —                                                                                                                                        | table `SLUG NAME CREATED`                                                                                                            |
| Tenancy | `org create --name <n> [--slug <s>]`                    | none           | `--name` required; `--slug` is generated when omitted                                                                                    | `Created org <slug> (<uuid>)`                                                                                                        |
| Tenancy | `project list`                                          | none           | —                                                                                                                                        | table `SLUG NAME CREATED`                                                                                                            |
| Tenancy | `project create --name <n> [--slug <s>]`                | none           | `--name` required; `--slug` is generated when omitted                                                                                    | `Created project <slug> (<uuid>)`                                                                                                    |
| Tenancy | `invite list`                                           | none           | —                                                                                                                                        | table `ID EMAIL ROLE STATUS EXPIRES`                                                                                                 |
| Tenancy | `invite send --email <a> [--role owner\|admin\|member]` | none           | `--email` required, `--role` defaults to `member`                                                                                        | `Invite queued: id=… email=… role=…`                                                                                                 |
| Tenancy | `invite revoke <id>`                                    | 1, uuid        | —                                                                                                                                        | `Revoked invite <uuid>`                                                                                                              |
| Meta    | `version`                                               | none           | —                                                                                                                                        | `opensheet <v> (commit <c>, built <t>)`                                                                                              |
| Meta    | `healthz`                                               | none           | `--timeout` (default `3s`)                                                                                                               | Probes `/healthz`; exits `0` on 2xx, `1` otherwise.                                                                                  |
| Meta    | `completion <bash\|zsh\|fish\|powershell>`              | 1, validated   | —                                                                                                                                        | Writes the completion script to stdout.                                                                                              |

- `init` creates the singleton org only in `selfhosted` mode (ignored in `cloud`). A blank
  `--org-slug` uses `tenant.singletonOrg.slug`, else a generated slug; an existing system org is reused.
- `serve` flag exclusions: `--no-scheduler`/`--scheduler-only`, `--no-consumer`/`--consumer-only`
  and `--consumer-only`/`--scheduler-only`. `--no-consumer` keeps full HTTP and stops consuming
  jobs; `--consumer-only` serves health probes only, consumes jobs and runs no scheduler, and fails
  boot with `queue.enabled=false`. `--scheduler-only` runs no consumer. What each combination
  runs: [`queue`](../queue/README.md#serve-flags).
- `scheduler run` exits `5` on an unknown job (`scheduler.unknown_job`), `6` when it is already
  running here or another replica holds the lock, and `7` when the runner is draining or
  `scheduler.enabled=false` (`scheduler.disabled`).
- `auth login` signs in in-process: it prints the Othra URL, tries to open the browser, waits up to
  five minutes for one loopback callback on `127.0.0.1` (`oidc.redirectPort`, `0` for a free port),
  then writes the session file. It needs `oidc.cliClientID`, a native public client; the web
  client's secret never leaves the server.
- `auth login --admin` is break-glass: it forces local login even when OIDC is configured, with a
  warning on stderr. `--email` + `--password-stdin` is the non-interactive local path; the password
  is read unmasked from stdin.
- `healthz` text output is `ok <url>  (<status>, <elapsed>)` or
  `fail <url>  (<status\|error>, <elapsed>)`. It is the same binary used as the compose and k8s
  healthcheck, so it needs no shell. `/healthz`, `/readyz` and `/robots.txt` are mounted on the
  **outer** mux at root under the `Probes` chain, never under `http.basePath` — probe URLs must not
  move when the app is remounted.

## Domain

### todo — control plane (S2)

| Command               | Args                | Flags                                   | Prints                       |
| --------------------- | ------------------- | --------------------------------------- | ---------------------------- |
| `todo list`           | none                | `--done`, `--open` — mutually exclusive | table `ID DONE TITLE`        |
| `todo add <title...>` | 1+, joined by space | —                                       | `Added todo <uuid>: <title>` |
| `todo toggle <id>`    | 1, uuid             | —                                       | `<uuid> done=<bool>`         |
| `todo delete <id>`    | 1, uuid             | —                                       | `Deleted todo <uuid>`        |

Omit both filters to list everything. Filtering happens client-side after the RPC returns.

### blog — data plane (S3)

Speaks REST with an API key, so it is the reference for what an integrator's own tooling does.
`--org` and `--project` name the path.

| Command                 | Args | Flags                                                                               |
| ----------------------- | ---- | ----------------------------------------------------------------------------------- |
| `blog list`             | none | —                                                                                   |
| `blog get <slug>`       | 1    | —                                                                                   |
| `blog create`           | none | `--title`, `--slug`, `--category` (all required), `--body`, `--idempotency-key`     |
| `blog update <slug>`    | 1    | `--if-version` (required), `--title`, `--slug`, `--body`, `--category`, `--replace` |
| `blog publish <slug>`   | 1    | `--if-version` (required)                                                           |
| `blog unpublish <slug>` | 1    | `--if-version` (required)                                                           |
| `blog delete <slug>`    | 1    | `--if-version` (required)                                                           |

- `blog list` text output is a table `SLUG STATUS VERSION TITLE`; every other blog command prints
  `id / slug / title / status / version / category`, one field per line.
- `--if-version` is the `version` last read with `blog get`. A post that moved on since exits `7`;
  a missing `--if-version` the server demands also exits `7`.
- `blog update` sends only the flags you changed. `--replace` overwrites the post wholesale (PUT).
- `blog publish` makes a post readable without a credential where the instance enables public
  reads; `unpublish` takes that back.

**`--idempotency-key` bounds the duplicate window, it does not remove it.** While the instance
that served the first call still holds the key, a retry with the same key and body replays that
first result; a different body under the same key exits `6`, as does a retry that arrives while
the first call is still running. The record is per-process: not shared between replicas, not
durable across a restart, and capped at 4096 recent keys for 24 hours. A retry landing elsewhere,
after a restart, or after eviction is served as a new request.

## Payload fields

| Command                  | Fields under `data`                                                   |
| ------------------------ | --------------------------------------------------------------------- |
| `scheduler list`         | `name`, `scope`, `schedule`, `timeout`, `singleton`                   |
| `auth whoami`            | `user_id`, `email`, `name`, `source`, `session_path`                  |
| `org list`               | `id`, `slug`, `name`, `owner_id`, `created_at`                        |
| `project list`           | `id`, `org_id`, `slug`, `name`, `created_at`                          |
| `invite list`            | `id`, `org_id`, `email`, `role`, `status`, `expires_at`, `created_at` |
| `todo list`              | `id`, `project_id`, `title`, `done`, `created_at`                     |
| `blog list` / `blog get` | `id`, `slug`, `title`, `body`, `categoryId`, `status`, `version`      |
| `version`                | `version`, `commit`, `buildTime`                                      |
| `healthz`                | `url`, `status`, `ok`, `took`, `error`                                |

Timestamps are RFC 3339 in UTC (`Z`), and table dates are UTC days. `role` is `owner\|admin\|member`; `status` is `pending\|accepted` for an
invite and `draft\|published` for a post. `blog` is the one payload using camelCase, because it
mirrors the data plane's JSON verbatim.
