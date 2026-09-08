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
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}          # the tab's rows as a JSON array
DELETE /api/v1/orgs/{org}/projects/{project}/sheets/{slug}/cache    # drop this sheet's snapshots
```

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
