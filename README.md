# opensheet

Publish a Google Sheets tab as a JSON API. A project connects a Google account,
registers a spreadsheet, publishes one of its tabs under a slug, and mints
scoped API keys; reads are served from a cached snapshot. Multitenant
(org → project), single process, single HTTP listener.
Module path: `altalune.id/opensheet`. Binary: `opensheet`.

## Quick start

Local binary (SQLite, single genesis admin):

```bash
make build
OPENSHEET_GENESIS_EMAIL=admin@local OPENSHEET_GENESIS_PASSWORD=change-me ./bin/opensheet serve
# first boot logs a one-time /onboard URL; afterwards sign in at
# http://127.0.0.1:5150/login
```

Full stack (Postgres + Mailpit + opensheet) via `compose.yaml`:

```bash
make compose-up
# opensheet:  http://127.0.0.1:5150/  (same one-time /onboard URL in the logs)
# mailpit:  http://127.0.0.1:8025    (every outbound email lands here)
```

Cloud config (Postgres + OIDC):

```bash
cp config.example.yaml config.yaml    # edit
make build
./bin/opensheet -c config.yaml serve
```

## Layout

```
opensheet/
├── api/                # buf-managed proto sources
├── authl/              # RFC 8252 OIDC PKCE loopback (exported)
├── cmd/                # opensheet main + code generators (gen-config-example, gen-tenant-tables, i18n-lint)
├── docs/               # configuration, deployment, multitenancy, error codes, CLI contract, templates
├── gen/                # generated proto (do not edit)
├── internal/
│   ├── api/            # Connect-RPC control plane
│   ├── apperror/       # stable error codes + Reporter fan-out
│   ├── auth/           # local + OIDC login orchestration
│   ├── boot/           # composition root
│   ├── cli/            # cobra command tree
│   ├── data/           # plain-HTTP JSON data plane
│   ├── gwerr/          # gworkspace error -> apperror adapter
│   ├── i18n/           # locale resolution + message catalogs
│   ├── legal/          # embedded terms + privacy policy
│   ├── password/       # argon2id hasher
│   ├── platform/       # authn, capabilities, config, db, notify, sealer, session, tenant, tokens
│   ├── testutil/       # hand-written fakes + pgtest harness
│   ├── web/            # templ + htmx handlers, middleware, icons, static
│   └── apikey/, credential/, invite/, onboard/, org/, project/, sheet/, spreadsheet/, todo/, user/   # domain modules
├── gworkspace/         # shared Google Workspace client layer + gsheet subpackage
├── httpclient/, logger/, mailer/, nanoid/, reqid/, scheduler/, telemetry/, worker/   # exported roots
├── schema/             # embedded goose migrations + RLS guard
├── scripts/            # smoke + module-shape checks
└── version/            # build-time version info
```

## Surfaces

Four ways in, one process, one listener. `internal/web` is also the outer mux —
it mounts the other two HTTP surfaces under `http.basePath`.

| Surface         | What it serves                                                                      | Authentication                                                                                                                       |
| --------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `internal/web`  | HTMX UI — the control plane a human uses, plus `/healthz`, `/readyz`, `/robots.txt` | Signed session cookie, HMAC-verified with `http.stateSecret`, principal loaded from the session store (`webmw.Session`)              |
| `internal/api`  | Connect-RPC control plane (gRPC-compatible + JSON) at `<basePath>/api/`             | `Authorization: Bearer <credential>` or `X-API-Key`, resolved by `authn.Chain` — an `osk_` API key, else a JWT via `tokens.Verifier` |
| `internal/data` | Plain-HTTP JSON data plane at `<basePath>/api/v1/`                                  | Same headers, but authorized per resource: the key must grant the scope **on that sheet**. A `public` sheet needs no credential      |
| `internal/cli`  | Cobra command tree — see [`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md)             | `--token` / `OPENSHEET_TOKEN` / `--token-file`, else the session file at `session.path` written by `opensheet auth login`            |

Data-plane routes, with `http.basePath` empty:

```
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}              # the tab's rows as a JSON array
POST   /api/v1/orgs/{org}/projects/{project}/sheets/{slug}              # append one row: {"values":[...]}
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}    # one row by id, with a row ETag
POST   /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows         # create one row: {"column":"value"}
POST   /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/batch   # create many rows: {"rows":[…]} — all or nothing
PUT    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}    # replace one row; an omitted known column is cleared
PATCH  /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}    # patch one row: {"column":"value"}
DELETE /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/rows/{id}    # soft delete: stamps deleted_at
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/capabilities # the tab's shape and contract state
DELETE /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/cache        # drop this sheet's snapshots
GET    /api/v1/orgs/{org}/projects/{project}/spreadsheets/{id}/tabs     # the document's tab titles
POST   /api/v1/orgs/{org}/projects/{project}/spreadsheets/{id}/tabs     # create a tab: {"title":"Q2"}
```

A write needs `sheets:write` plus `sheets.writable` on the sheet; the tabs routes
need `spreadsheets:read` / `spreadsheets:write`, and `spreadsheets.writable` to
create. They name no sheet, so a key restricted to specific sheets cannot use
them at all. `POST …/sheets/{slug}`, `POST …/rows` and `POST …/rows/batch`
honour `Idempotency-Key`; a retry under one key replays the first attempt's
response rather than writing again. `PATCH`, `PUT` and `DELETE` on a row honour
`If-Match` against the row's `ETag` — a stale tag is `412` and writes nothing,
an absent header is last-write-wins. `PUT` and `DELETE` are idempotent by
construction and need no key, and a delete of an already-deleted row is `204`.

The whole-tab `GET` takes three query parameters, and any one of them switches it
from serving the stored snapshot verbatim to querying the row projection:

```
?where=col:op:value   # repeated; every clause ANDs
?limit=100            # 1 to sheets.maxQueryRows
?cursor=<opaque>      # the keyset position carried by a Link header
```

The operators are `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `contains`, `starts`,
`in`, `empty` and `present`. Every comparison is text: `contains` and `starts`
are case-insensitive substring and prefix matches with `%` and `_` treated
literally, `in` takes a comma-separated list, and `empty`/`present` take no
value, so they are written `col:empty` with no trailing colon. Ordering is
lexical, which is right for ISO-8601 dates and zero-padded numbers and wrong for
unpadded ones (`"10" < "3"`); typed comparison and `?sort=` are not implemented.
A clause splits on the first two colons only, so a value may contain colons.

An unknown column, an unknown operator, a `limit` outside the cap and a
malformed `cursor` are each `400`. So is an unrecognised query parameter, rather
than being ignored, so a typo cannot silently serve the whole tab. Zero matches
is `200 []`.

Pagination is keyset over the tab's row order. A page with a successor carries
`Link: <?…&cursor=…>; rel="next"` — a relative URI-reference — and the absence of
that header is the only end-of-walk signal. `limit` is bounded by
`sheets.maxQueryRows` (default 1000), which is also the default when `limit` is
absent: a filtered read is never unbounded, because `sheets.maxPayloadBytes` does
not apply to it. The cursor carries the content digest it was issued against, so
a sheet edited mid-walk refuses it with `409` rather than silently skipping or
repeating rows, while a TTL refresh that changed nothing leaves the digest alone
and the walk continues.

A filtered read has its own `ETag`, hashed from the sheet's generation and the
canonical query, so it can never collide with an unfiltered tag, and clause order
does not move it. `If-None-Match` returns `304` with that `ETag` and
`Cache-Control` but **no** `Link`: the tag is evaluated before the query runs, so
no next cursor exists yet, and a walking client already holds one from the
preceding `200`.

The two paths part company on an over-cap tab. `sheets.maxPayloadBytes` bounds
the stored snapshot, not a filtered body, and the projection is written before
the payload is marshalled — so a tab over that cap is fully queryable while the
unfiltered read of the same sheet still returns `413`.

`…/capabilities` is authorized like the rows `GET` (`sheets:read`, or public
visibility) and is answered from the row projection and the `sheets` row alone —
no Google call — so it is cheap to poll. It reports the tab's columns, whether it
has an `id` column, its live row count, and whether it still satisfies the table
contract. Publishing a tab now reads it once and fails closed if it has no `id`
column; `capabilities` is a reserved slug.

`api.enabled` gates the RPC surface only — the data plane is always mounted.

## Exported packages

Safe for external Go projects to import:

| Package      | Purpose                                                                                               |
| ------------ | ----------------------------------------------------------------------------------------------------- |
| `authl`      | OIDC client + PKCE loopback                                                                           |
| `reqid`      | UUIDv7 request-ID propagation                                                                         |
| `nanoid`     | URL-safe short-ID generator of caller-chosen length, plus invite tokens                               |
| `httpclient` | Outbound HTTP clients — SSRF-filtered, traced and timeout-bounded by default                          |
| `worker`     | Supervisor + Worker interface + HTTP/Func adapters                                                    |
| `scheduler`  | Cron/interval job runner — system and per-tenant scope, leader election                               |
| `logger`     | `slog.Handler` — auto-attaches request_id/trace_id, key redaction                                     |
| `telemetry`  | OTel tracer + meter + Prometheus reader                                                               |
| `mailer`     | Transactional mail — `console`, `smtp`, `resend` drivers                                              |
| `gworkspace` | Google Workspace clients — shared transport + typed provider errors; `gworkspace/gsheet` reads Sheets |

Pre-1.0.0: minor releases may break; pin exact versions. Post-1.0.0:
exported surface is frozen, additive changes only. Everything under
`internal/` is private.

## Sign-up and invitation policies

| Mode                | Sign-in path                                  | Behavior                                                                                     |
| ------------------- | --------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Selfhosted, no OIDC | Local `/login` (genesis + password-set users) | Works. No invites, no T&C step.                                                              |
| Selfhosted, no OIDC | `POST /orgs/{slug}/invites`                   | Blocked with 409; invites banner shown, form hidden.                                         |
| Selfhosted + OIDC   | Uninvited OIDC sign-in                        | Rejected before persistence; renders a 403 "not invited" page.                               |
| Selfhosted + OIDC   | Invited OIDC sign-in                          | User created, membership from invite, `/welcome` (T&C), then dashboard.                      |
| Cloud (OIDC forced) | Invited OIDC sign-in                          | Same as selfhosted + OIDC invited.                                                           |
| Cloud (OIDC forced) | Uninvited OIDC sign-in                        | User created, no silent org, redirect to `/signup/complete` to name the org + first project. |

Invite issuance requires `mode=cloud` or `oidc.issuer` set. `/signup/complete` runs only in cloud mode; the T&C checkbox appears when `compliance.requireAcceptance=true`.

## Documentation

| Doc                                                            | For                                                 |
| -------------------------------------------------------------- | --------------------------------------------------- |
| [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md)               | precedence, awareness tags, modes, first-boot       |
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)                     | docker, DB roles, RLS, replica, observability, OIDC |
| [`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md)                 | stable command tree, exit codes, output envelopes   |
| [`docs/MULTITENANCY.md`](docs/MULTITENANCY.md)                 | modes, tenant isolation, sign-in routing            |
| [`docs/MULTITENANCY_CONTEXT.md`](docs/MULTITENANCY_CONTEXT.md) | how a request gets its tenant scope                 |
| [`docs/ERROR_CODES.md`](docs/ERROR_CODES.md)                   | the `<DOM><NNN>` code registry, append-only         |
| [`docs/MODULE_TEMPLATE.md`](docs/MODULE_TEMPLATE.md)           | adding a domain module                              |
| [`docs/PLATFORM_TEMPLATE.md`](docs/PLATFORM_TEMPLATE.md)       | adding a platform primitive                         |
| [`schema/README.md`](schema/README.md)                         | migrations, the runtime migrator, RLS boot guard    |
| [`gworkspace/README.md`](gworkspace/README.md)                 | Google clients, OAuth notes, header handling        |
| [`CONTRIBUTING.md`](CONTRIBUTING.md)                           | workflow, testing, release                          |
| [`GLOSSARY.md`](GLOSSARY.md)                                   | one concept, one name — canonical terminology       |
| [`AGENTS.md`](AGENTS.md)                                       | rules for AI coding agents                          |

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
