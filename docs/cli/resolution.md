# CLI flags and resolution

Global flags, profiles, and how a token or a URL is picked. Surface and exit codes: [`cli`](README.md).
Commands: [`commands`](commands.md).

## Global flags

Persistent on root, so every command accepts them. Only some read them.

| Flag               | Env                    | Default                                       | Read by                                                                     |
| ------------------ | ---------------------- | --------------------------------------------- | --------------------------------------------------------------------------- |
| `-c, --config`     | —                      | discover `opensheet.yaml` in `.` then `$HOME` | every command, via `config.Load`                                            |
| `--url`            | `OPENSHEET_URL`        | the profile's URL, then `http.baseURL`        | `auth login` (picks remote sign-in), `auth whoami`, `todo`, `healthz`       |
| `--token`          | `OPENSHEET_TOKEN`      | —                                             | `auth whoami`, `todo`                                                       |
| `--token-file`     | —                      | —                                             | same as `--token`; the file holds the token alone                           |
| `--profile`        | `OPENSHEET_PROFILE`    | `default`                                     | `auth`, `todo`, and the in-process commands, which act as its local sign-in |
| `--output`         | `OPENSHEET_OUTPUT`     | text on a TTY, json off-TTY                   | every command that prints a payload                                         |
| `--org`            | `OPENSHEET_ORG`        | —                                             | nothing — declared, not wired                                               |
| `--project`        | `OPENSHEET_PROJECT`    | —                                             | nothing — declared, not wired                                               |
| `--no-interactive` | —                      | `false`                                       | nothing — declared, not wired                                               |
| `--log-level`      | `OPENSHEET_LOG_LEVEL`  | `info`                                        | nothing; the **env var** works, via `log.level` in config                   |
| `--log-format`     | `OPENSHEET_LOG_FORMAT` | `json`                                        | nothing; the **env var** works, via `log.format` in config                  |
| `-v, --version`    | —                      | —                                             | root, prints the version string and exits                                   |

`--org` / `--project` are **not** global tenant overrides. Every tenant-scoped command takes its org
and project from the principal and ignores both flags. See
[Declared but not wired](README.md#declared-but-not-wired).

Config keys and their `OPENSHEET_*` spellings: [`config`](../config/README.md).

## Profiles

A profile is one sign-in, kept by name in the session file at `session.path` (default
`~/.opensheet/session.json`). The name is, first hit wins:

```
--profile > OPENSHEET_PROFILE > "default"
```

| Kind   | Written by                 | Holds                                                                                                                       | Used by                                   |
| ------ | -------------------------- | --------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------- |
| remote | `auth login --url <url>`   | the URL, Othra's issuer, the CLI client id, the resource, access and refresh tokens, expiry, scopes, the `Whoami` principal | `auth whoami`, `todo`                     |
| local  | `auth login` without a URL | the principal of this server's database; no URL and no token                                                                | `org`, `project`, `invite`, `auth whoami` |

- The file is `{"version": 1, "profiles": {"<name>": {…}}}`. An older format loads as empty, so
  its people sign in again; a newer one is an error, so an older binary never overwrites it.
- The directory is `0700` and the file `0600`; a file others can read is refused until it is
  `chmod 600` again. A save writes a fresh temp file, syncs it and renames it into place, so a
  crash never leaves half a file.
- Every write, and every token refresh, holds an exclusive `flock` on `<session.path>.lock`
  (linux and darwin only). Waiting for it stops on Ctrl-C.

## Credential resolution

Token, first hit wins (`internal/cli/token.go`, `internal/cli/profile.go`):

```
--token > --token-file > OPENSHEET_TOKEN > the profile's token
```

- An explicit token (an API key, an `othra_pt_` personal token or an access token) is sent as-is
  to whatever URL resolves, and never refreshed. It reads the session file only to find a URL when
  none was given, and ignores a broken file.
- A profile's token goes only to the profile's own URL. Another URL fails with
  `ProfileHostMismatchError` (exit `2`) and nothing is sent.
- A local profile, or none, on a remote command fails with `NotSignedInError` (exit `2`), hinting
  `auth login --url`.
- The access token is refreshed (RFC 6749 Section 6) when it expires within 60 seconds, or when
  the issuer sent no `expires_in` but a refresh token. The refresh runs under the lock and re-reads the profile first,
  so a second process uses the token the first one stored. Othra revokes the whole grant when a
  refresh token is spent twice, so the lock is what keeps two commands from signing each other out.
- `invalid_grant` (expired, revoked or reused) clears the profile's token, keeps its URL, and
  fails with `SessionExpiredError` (exit `2`): `run opensheet auth login --url <url> --profile <p>`.
- A network error or a 5xx keeps the tokens and exits `1`, so an Othra outage signs no one out.
- When the issuer rotates the refresh token but the response fails a check, the new refresh token
  is still stored (RFC 9700 Section 4.14.2). A refresh already sent finishes and is stored even
  when the command is interrupted.
- With no `expires_in` and no refresh token, the access token is used until the server refuses it.
- A credential the server rejects exits `2` with a hint to sign in or pass `--token`.

## URL resolution

URL, first hit wins (`internal/cli/url.go`):

```
--url > OPENSHEET_URL > the profile's URL > http.baseURL from config
```

No URL at all exits `4` (`MissingURLError`).

## In-process commands

`init`, `serve`, `migrate`, `scheduler`, `org`, `project` and `invite` boot this server's
database. They refuse `--url`, `--token` and `--token-file` with exit `4` before booting
anything (`RemoteFlagError`). `org`, `project` and `invite` act as the local profile's principal;
`--profile` picks which one. A remote profile is refused there: its ids belong to another server.

`auth login` without a URL is in-process too. It takes no `--token` or `--token-file` either way.

## healthz

`healthz` stops after two legs: `--url` > `OPENSHEET_URL` > a target derived from `http.addr`
(`http://127.0.0.1:<port>/healthz`). It never adopts a profile or `http.baseURL`. A liveness probe
must reach the listener it runs beside, and `http.baseURL` is `required`, so a container setting it
to its public domain would otherwise probe the load balancer and report healthy while this instance
is dead. A `--url` whose path is empty or `/` gets `/healthz` appended; one that already names a
path is probed as given.

> **SECURITY: a profile's token is bound to the URL it signed in to.** When `--url` names another
> server, the profile's token is never sent. Only an explicit `--token` / `--token-file` /
> `OPENSHEET_TOKEN` — unambiguous operator intent — goes to whatever server `--url` names.
