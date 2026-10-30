# CLI commands

Every command, its args, flags, printed contract and payload fields. Surface and exit codes:
[`cli`](README.md). Global flags: [`resolution`](resolution.md).

## Commands

Group is the `--help` heading. `project` and `invite` act on the caller's active org, taken from
the session principal — **not** from `--org`.

| Group   | Command                                                 | Args           | Flags                                                                                                                                    | Contract / prints                                                                                                                               |
| ------- | ------------------------------------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime | `init`                                                  | none           | `--email` (required), `--name` (defaults to `--email`), `--org-slug` (empty: generated), `--org-name`, `--project-slug`, `--interactive` | First-run bootstrap: creates the genesis admin and marks onboarding complete. Exits `6` when already onboarded (`ONB002`).                      |
| Runtime | `serve`                                                 | none           | `--no-scheduler`, `--scheduler-only`, `--no-consumer`, `--consumer-only` — exclusions below                                              | Runs the HTTP listener on `http.addr` and blocks until signalled.                                                                               |
| Runtime | `migrate up`                                            | none           | —                                                                                                                                        | Applies every pending migration. Prints `migrations: up-to-date`.                                                                               |
| Runtime | `migrate status`                                        | none           | —                                                                                                                                        | One row per migration: `<version> <applied-at \| "pending"> <source>`.                                                                          |
| Runtime | `migrate down-to <version>`                             | exactly 1, int | —                                                                                                                                        | Rolls the schema back to that goose version.                                                                                                    |
| Runtime | `scheduler list`                                        | none           | —                                                                                                                                        | Lists registered jobs from the wired runner. Runs nothing.                                                                                      |
| Runtime | `scheduler run <job>`                                   | exactly 1      | —                                                                                                                                        | Runs one job now, bypassing its schedule. Tenant-scoped jobs still fan out; singletons still take the cross-process lock.                       |
| Auth    | `auth login`                                            | none           | `--admin`, `--email`, `--password-stdin` — local sign-in only                                                                            | With `--url` / `OPENSHEET_URL`: remote Othra sign-in, saved as a remote profile. Otherwise in-process, saved as a local profile. Details below. |
| Auth    | `auth logout`                                           | none           | —                                                                                                                                        | Revokes the profile's refresh token at Othra (RFC 7009), then removes the profile. `Signed out of <url> (profile <p>)`.                         |
| Auth    | `auth whoami`                                           | none           | —                                                                                                                                        | A local profile prints its stored principal; otherwise asks the server's `Whoami`. One `key: value` line per non-empty field.                   |
| Tenancy | `org list`                                              | none           | —                                                                                                                                        | table `SLUG NAME CREATED`                                                                                                                       |
| Tenancy | `org create --name <n> [--slug <s>]`                    | none           | `--name` required; `--slug` is generated when omitted                                                                                    | `Created org <slug> (<uuid>)`                                                                                                                   |
| Tenancy | `project list`                                          | none           | —                                                                                                                                        | table `SLUG NAME CREATED`                                                                                                                       |
| Tenancy | `project create --name <n> [--slug <s>]`                | none           | `--name` required; `--slug` is generated when omitted                                                                                    | `Created project <slug> (<uuid>)`                                                                                                               |
| Tenancy | `invite list`                                           | none           | —                                                                                                                                        | table `ID EMAIL ROLE STATUS EXPIRES`                                                                                                            |
| Tenancy | `invite send --email <a> [--role owner\|admin\|member]` | none           | `--email` required, `--role` defaults to `member`                                                                                        | `Invite queued: id=… email=… role=…`                                                                                                            |
| Tenancy | `invite revoke <id>`                                    | 1, uuid        | —                                                                                                                                        | `Revoked invite <uuid>`                                                                                                                         |
| Meta    | `version`                                               | none           | —                                                                                                                                        | `opensheet <v> (commit <c>, built <t>)`                                                                                                         |
| Meta    | `healthz`                                               | none           | `--timeout` (default `3s`)                                                                                                               | Probes `/healthz`; exits `0` on 2xx, `1` otherwise.                                                                                             |
| Meta    | `completion <bash\|zsh\|fish\|powershell>`              | 1, validated   | —                                                                                                                                        | Writes the completion script to stdout.                                                                                                         |

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
- `auth login --url <url> [--profile <p>]` signs in to a remote server through Othra, as a native
  public client (RFC 8252, PKCE):
  1. It reads `<url>`'s API RFC 9728 document, `/.well-known/oauth-protected-resource{basePath}/api`.
     The document must name the resource `<url>/api` exactly, one authorization server, and a
     `cli_client_id` (the server's `oidc.cliClientID`). `<url>` must be https, or http on a
     loopback host, with no userinfo, query or fragment. The issuer must be https; plain http
     only on a loopback IP literal, and only when `<url>` is on loopback too.
  2. It prints `Signing in through <issuer>` and the Othra URL, tries to open the browser, and waits up to five minutes for one
     loopback callback on `127.0.0.1` (`oidc.redirectPort`, `0` for a free port). It asks for
     `openid offline_access` plus the document's `scopes_supported`, with `resource=<url>/api`
     (RFC 8707), so the access token is bound to the API. Othra narrows the scopes to the CLI
     client's grant; `auth whoami` and `todo` need `apikeys:read`.
  3. It calls `Whoami` with the new token and saves the profile: the URL, issuer, client id,
     resource, tokens, expiry, scopes and principal. Signing in again to a profile first revokes
     the refresh token it replaces. It prints
     `Signed in to <url> as <email> (profile <p>)`.
  - A person signs in to the app on the web once before the CLI works. Until then `Whoami` answers
    `401`: the CLI revokes the new refresh token, saves nothing and exits `2` with that hint. Any
    other `Whoami` failure is a warning, and the profile is saved.
- `auth login` without a URL signs in in-process: the same loopback on `oidc.cliClientID` when
  `oidc.issuer` is set, the local genesis prompt otherwise. It saves a local profile and prints
  `Signed in as <email> (<source>, profile <p>)`. The web client's secret never leaves the server.
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

Registered only when `published.Todo` is on; opensheet ships it off.

| Command               | Args                | Flags                                   | Prints                       |
| --------------------- | ------------------- | --------------------------------------- | ---------------------------- |
| `todo list`           | none                | `--done`, `--open` — mutually exclusive | table `ID DONE TITLE`        |
| `todo add <title...>` | 1+, joined by space | —                                       | `Added todo <uuid>: <title>` |
| `todo toggle <id>`    | 1, uuid             | —                                       | `<uuid> done=<bool>`         |
| `todo delete <id>`    | 1, uuid             | —                                       | `Deleted todo <uuid>`        |

Omit both filters to list everything. Filtering happens client-side after the RPC returns.
`todo list` and `todo add` act in the project the server's `Whoami` names (`active_project_id`).

## Payload fields

| Command          | Fields under `data`                                                                                                       |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `scheduler list` | `name`, `scope`, `schedule`, `timeout`, `singleton`                                                                       |
| `auth whoami`    | `profile`, `url`, `source`, `user_id`, `email`, `name`, `active_org_id`, `active_org_slug`, `active_project_id`, `scopes` |
| `org list`       | `id`, `slug`, `name`, `owner_id`, `created_at`                                                                            |
| `project list`   | `id`, `org_id`, `slug`, `name`, `created_at`                                                                              |
| `invite list`    | `id`, `org_id`, `email`, `role`, `status`, `expires_at`, `created_at`                                                     |
| `todo list`      | `id`, `project_id`, `title`, `done`, `created_at`                                                                         |
| `version`        | `version`, `commit`, `buildTime`                                                                                          |
| `healthz`        | `url`, `status`, `ok`, `took`, `error`                                                                                    |

Timestamps are RFC 3339 in UTC (`Z`), and table dates are UTC days. `role` is `owner\|admin\|member`; `status` is `pending\|accepted` for an
invite.
