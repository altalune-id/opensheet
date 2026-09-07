# altempl

Reference multitenant Go template — Templ + HTMX SSR + Connect-RPC on one HTTP
listener. Module path: `altalune.id/template`. Binary: `altempl`.

## Quick start

Local binary (SQLite, single genesis admin):

```bash
make build
ALT_GENESIS_EMAIL=admin@local ALT_GENESIS_PASSWORD=change-me ./bin/altempl serve
# open http://127.0.0.1:5150/login
```

Full stack (Postgres + Mailpit + altempl) via `compose.yaml`:

```bash
make compose-up
# altempl:  http://127.0.0.1:5150/login
# mailpit:  http://127.0.0.1:8025    (every outbound email lands here)
```

Cloud config (Postgres + OIDC):

```bash
cp config.example.yaml config.yaml    # edit
make build
./bin/altempl -c config.yaml serve
```

## Layout

```
altempl/
├── api/                # buf-managed proto sources
├── authl/              # RFC 8252 OIDC PKCE loopback (exported)
├── cmd/altempl/        # main package
├── docs/               # configuration, deployment, CLI contract, module & platform templates
├── gen/                # generated proto (do not edit)
├── internal/
│   ├── apperror/       # stable error codes + Reporter fan-out
│   ├── auth/           # local + OIDC login orchestration
│   ├── boot/           # composition root
│   ├── cli/            # cobra command tree
│   ├── invite/, org/, project/, todo/, user/    # domain modules
│   ├── platform/       # capabilities, config, db, notify, session, tenant, tokens
│   └── web/            # templ + htmx handlers, icons, i18n
├── logger/, mailer/, nanoid/, reqid/, scheduler/, telemetry/, worker/   # exported roots
├── schema/             # embedded goose migrations + RLS guard
└── version/            # build-time version info
```

## Exported packages

Safe for external Go projects to import:

| Package     | Purpose                                                                 |
| ----------- | ----------------------------------------------------------------------- |
| `authl`     | OIDC client + PKCE loopback                                             |
| `reqid`     | UUIDv7 request-ID propagation                                           |
| `nanoid`    | 21-char nanoid generator                                                |
| `worker`    | Supervisor + Worker interface + HTTP/Func adapters                      |
| `scheduler` | Cron/interval job runner — system and per-tenant scope, leader election |
| `logger`    | `slog.Handler` — auto-attaches request_id/trace_id, key redaction       |
| `telemetry` | OTel tracer + meter + Prometheus reader                                 |
| `mailer`    | Transactional mail — `console`, `smtp`, `resend` drivers                |

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

| Doc                                                      | For                                                 |
| -------------------------------------------------------- | --------------------------------------------------- |
| [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md)         | precedence, awareness tags, modes, first-boot       |
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)               | docker, DB roles, RLS, replica, observability, OIDC |
| [`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md)           | stable command tree, exit codes, output envelopes   |
| [`docs/MODULE_TEMPLATE.md`](docs/MODULE_TEMPLATE.md)     | adding a domain module                              |
| [`docs/PLATFORM_TEMPLATE.md`](docs/PLATFORM_TEMPLATE.md) | adding a platform primitive                         |
| [`CONTRIBUTING.md`](CONTRIBUTING.md)                     | workflow, testing, release                          |
| [`GLOSSARY.md`](GLOSSARY.md)                             | one concept, one name — canonical terminology       |
| [`AGENTS.md`](AGENTS.md)                                 | rules for AI coding agents                          |

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
