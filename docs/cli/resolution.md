# CLI flags and resolution

Global flags, and how a token or a URL is picked. Surface and exit codes: [`cli`](README.md).
Commands: [`commands`](commands.md).

## Global flags

Persistent on root, so every command accepts them. Only some read them.

| Flag               | Env                    | Default                                       | Read by                                                    |
| ------------------ | ---------------------- | --------------------------------------------- | ---------------------------------------------------------- |
| `-c, --config`     | —                      | discover `opensheet.yaml` in `.` then `$HOME` | every command, via `config.Load`                           |
| `--url`            | `OPENSHEET_URL`        | `http.baseURL` from config                    | `blog`, `healthz`                                          |
| `--token`          | `OPENSHEET_TOKEN`      | —                                             | every command that resolves a principal                    |
| `--token-file`     | `OPENSHEET_TOKEN_FILE` | —                                             | same, file must be mode 0600                               |
| `--output`         | `OPENSHEET_OUTPUT`     | text on a TTY, json off-TTY                   | every command that prints a payload                        |
| `--org`            | `OPENSHEET_ORG`        | —                                             | **`blog` only**                                            |
| `--project`        | `OPENSHEET_PROJECT`    | —                                             | **`blog` only**                                            |
| `--no-interactive` | —                      | `false`                                       | nothing — declared, not wired                              |
| `--log-level`      | `OPENSHEET_LOG_LEVEL`  | `info`                                        | nothing; the **env var** works, via `log.level` in config  |
| `--log-format`     | `OPENSHEET_LOG_FORMAT` | `json`                                        | nothing; the **env var** works, via `log.format` in config |
| `-v, --version`    | —                      | —                                             | root, prints the version string and exits                  |

`--org` / `--project` are **not** global tenant overrides. Every other tenant-scoped command takes
its org and project from the session principal and ignores both flags. Treat them as `blog`'s path
arguments. See [Declared but not wired](README.md#declared-but-not-wired).

Config keys and their `OPENSHEET_*` spellings: [`config`](../config/README.md).

## Credential and URL resolution

Token, in order, first hit wins (`internal/cli/principal.go`):

```
--token > OPENSHEET_TOKEN > --token-file > OPENSHEET_TOKEN_FILE > session.path file > interactive login
```

A token supplied explicitly is verified against the server's `Whoami` when `http.baseURL` is set.
If the server rejects it the command exits `2` — it never falls back to a lower-precedence source.

URL, in order (`internal/cli/url.go`):

```
--url > OPENSHEET_URL > the sole saved profile > http.baseURL from config
```

A saved profile is used only when exactly one instance is on record; more than one makes the
default ambiguous, so none is chosen.

`healthz` stops after two legs: `--url` > `OPENSHEET_URL` > a target derived from `http.addr`
(`http://127.0.0.1:<port>/healthz`). It never adopts a profile or `http.baseURL`. A liveness probe
must reach the listener it runs beside, and `http.baseURL` is `required`, so a container setting it
to its public domain would otherwise probe the load balancer and report healthy while this instance
is dead. A `--url` whose path is empty or `/` gets `/healthz` appended; one that already names a
path is probed as given.

> **SECURITY: a device credential is bound to the URL it was issued for.** When `--url` names a
> different host than the saved profile, that profile's credential is never sent. Only an explicit
> `--token` / `OPENSHEET_TOKEN` / `--token-file` / `OPENSHEET_TOKEN_FILE` — unambiguous operator intent — goes
> to whatever host `--url` names. A profile recorded before URL-keying carries no URL; it is
> adopted for the target URL and rewritten, never treated as a mismatch.
