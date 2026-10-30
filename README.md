# opensheet

Publish a Google Sheets tab as a JSON API. A project connects a Google account,
registers a spreadsheet, publishes one of its tabs under a slug, and mints
scoped API keys; reads are served from a cached snapshot, and rows can be
created, replaced, patched and soft-deleted. Multitenant (org → project), single
process, single HTTP listener.

Built on the altalune multitenant Go template. Module path:
`altalune.id/opensheet`. Binary: `opensheet`.

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

`make help` lists every target. `make check` is the pre-commit gate; releases are cut with
GoReleaser from `.goreleaser.yaml` — see [`CONTRIBUTING.md`](CONTRIBUTING.md#releasing).

## Layout

```
opensheet/
├── api/                # buf-managed proto sources, incl. the (mcp.v1.tool) annotation
├── cmd/opensheet/        # main package
├── cmd/protoc-gen-mcp/ # protoc plugin: annotated RPCs → MCP tool registrations
├── cmd/…               # build-time tools: comment-lint, i18n-lint, gen-config-example, gen-tenant-tables
├── docs/               # architecture, surfaces, scopes, MCP, configuration, deployment, CLI contract, templates
├── gen/                # generated proto (do not edit)
├── internal/
│   ├── apperror/       # stable error codes + Reporter fan-out
│   ├── auth/           # local + OIDC login orchestration
│   ├── boot/           # composition root
│   ├── cli/            # cobra command tree — the cli surface (S6)
│   ├── apikey/, blog/, invite/, onboard/, org/, project/, todo/, user/   # domain modules (blog, todo: example code, unmounted)
│   ├── credential/, spreadsheet/, sheet/   # opensheet domain modules: Google account, Google file, published tab
│   ├── gwerr/          # opensheet: gworkspace error -> apperror adapter
│   ├── legal/          # embedded Terms of Service + Privacy Policy markdown
│   ├── controlplane/   # Connect-RPC surface (S2), mounted at /api/
│   ├── dataplane/      # REST surface for integrators (S3), mounted at /api/v1/
│   ├── ingest/         # inbound provider webhooks (S4), mounted at /hooks/
│   ├── mcp/            # MCP surface (S7): auth, scope catalog, metadata, the Apps UI bundle
│   ├── i18n/           # locale bundles + the SSR locale middleware
│   ├── password/       # argon2id hashing
│   ├── platform/       # authn, capabilities, config, db, notify, outbox, sealer, session, surfaces, tenant, tokens
│   ├── testutil/       # hand-written fakes and test harnesses (no mocks)
│   └── web/            # console surface (S1): templ + htmx handlers, icons, static assets
├── logger/, mailer/, nanoid/, reqid/, scheduler/, telemetry/, worker/   # exported roots
├── httpclient/, mcp/   # exported roots: SSRF-guarded HTTP client, MCP server runtime
├── gworkspace/         # opensheet-only exported root: Google Workspace clients + gsheet
├── schema/             # embedded goose migrations + RLS guard
├── scripts/            # vendoring, templ normalization, DB provisioning, smoke checks
├── skills/             # opensheet-api: the integrator skill for the data plane
└── version/            # build-time version info
```

## Data plane

The product is the data plane (S3) at `<basePath>/api/v1/`, authenticated by an `osk_` API key
whose scopes and sheet grant are checked per request. A `public` sheet needs no key while
`sheets.publicEnabled` is on.

```
GET    /api/v1/orgs/{org}/projects/{project}/sheets/{slug}              # the tab's rows; ?where= ?sort= ?limit= ?cursor=
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

Write rules, query syntax, paging and caching: [`docs/surfaces/opensheet.md`](docs/surfaces/opensheet.md).
For integrators: [`skills/opensheet-api`](skills/opensheet-api/SKILL.md).

## Exported packages

Safe for external Go projects to import:

| Package      | Purpose                                                                                               |
| ------------ | ----------------------------------------------------------------------------------------------------- |
| `reqid`      | UUIDv7 request-ID propagation                                                                         |
| `nanoid`     | URL-safe short-ID generator of caller-chosen length, plus invite tokens                               |
| `worker`     | Supervisor + Worker interface + HTTP/Func adapters                                                    |
| `scheduler`  | Cron/interval job runner — system and per-tenant scope, leader election                               |
| `logger`     | `slog.Handler` — auto-attaches request_id/trace_id, key redaction                                     |
| `telemetry`  | OTel tracer + meter + Prometheus reader                                                               |
| `mailer`     | Transactional mail — `console`, `smtp`, `resend` drivers                                              |
| `httpclient` | Retrying HTTP client whose dialer refuses private and loopback addresses                              |
| `mcp`        | Scope-checked MCP tool registry over the MCP Go SDK's streamable-HTTP server                          |
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

opensheet's additions to a template doc sit next to it as `opensheet.md`: config, deployment,
errors, multitenancy, surfaces.

| Doc                                                   | For                                                  |
| ----------------------------------------------------- | ---------------------------------------------------- |
| [`config`](docs/config/README.md)                     | precedence, awareness tags, modes, first-boot        |
| [`deployment`](docs/deployment/README.md)             | docker, DB roles, RLS, replica, observability, OIDC  |
| [`cli`](docs/cli/README.md)                           | stable command tree, exit codes, output envelopes    |
| [`architecture`](docs/architecture/README.md)         | the layer map: surfaces, domain, data                |
| [`surfaces`](docs/surfaces/README.md)                 | the seven surfaces, their mounts, credentials, rules |
| [`multitenancy`](docs/multitenancy/README.md)         | what a tenant is, how the schema and RLS enforce it  |
| [`request scope`](docs/multitenancy/request-scope.md) | how a request gets its tenant scope                  |
| [`scopes`](docs/scopes/README.md)                     | the scope catalog integrators mint keys against      |
| [`mcp`](docs/mcp/README.md)                           | the MCP surface: mount, auth, tools, UI, testing     |
| [`error codes`](docs/errors/README.md)                | stable error codes and their envelopes               |
| [`modules`](docs/modules/README.md)                   | adding a domain module                               |
| [`platform`](docs/platform/README.md)                 | adding a platform primitive                          |
| [`schema/README.md`](schema/README.md)                | migrations, the runtime migrator, RLS boot guard     |
| [`gworkspace/README.md`](gworkspace/README.md)        | Google clients, OAuth notes, header handling         |
| [`CONTRIBUTING.md`](CONTRIBUTING.md)                  | workflow, testing, release                           |
| [`GLOSSARY.md`](GLOSSARY.md)                          | one concept, one name — canonical terminology        |
| [`AGENTS.md`](AGENTS.md)                              | rules for AI coding agents                           |

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
