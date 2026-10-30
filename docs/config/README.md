# Configuration

Typed in `internal/platform/config/`; `.env.example` and `config.example.yaml` are generated from
its struct tags and list **every** key. This page is the contract — precedence, awareness tags,
modes, first boot, and the keys whose behavior does not follow from the name. Adding one:
[`howto/config-key.md`](../howto/config-key.md); operating: [`deployment`](../deployment/README.md).
`make config-examples` regenerates both files; `make config-examples-check` fails CI on drift.

## Precedence

`defaults <- config.yaml <- OPENSHEET_* env`. Env always wins. A key's env var is its dotted path,
prefixed `ALT`, `.` and `-` → `_`, upper-cased:

| YAML                             | Env var                                    |
| -------------------------------- | ------------------------------------------ |
| `mode: cloud`                    | `OPENSHEET_MODE=cloud`                     |
| `http.basePath: /opensheet`      | `OPENSHEET_HTTP_BASE_PATH=/opensheet`      |
| `tenant.singletonOrg.slug: main` | `OPENSHEET_TENANT_SINGLETON_ORG_SLUG=main` |

Each segment is the **`mapstructure`** tag (`segmentFor` in `internal/platform/config/env.go`);
`yaml` mirrors it and is decorative. The config file is `-c/--config <path>`, else `opensheet.<ext>`
in the working directory, then `$HOME`; missing is fine unless `config.WithRequireFile()` is passed.

## Awareness tags

Awareness lives on the **struct**: an `awareness:"..."` tag on the field. `.env.example` is the
rendering — the tag is printed in `[brackets]` above each env var, and the file is split into a
BOOTSTRAP and a RUNTIME section.

| Value                            | Meaning                                                                   |
| -------------------------------- | ------------------------------------------------------------------------- |
| `required`                       | an operator must set it; the boot-time check lives in `config.Validate()` |
| `bootstrap`                      | locks in at first boot; changing later is a no-op on persisted data       |
| `secret`                         | never commit; `secret` fields never emit a default value                  |
| `mode:cloud` / `mode:selfhosted` | only meaningful in the named mode                                         |
| `-`                              | drop what the enclosing struct contributed (`encoding/json` convention)   |

- **Tags inherit down**, merged by `mergeAwareness` (`internal/platform/config/envwalk.go`):
  `Config.Genesis` is `bootstrap`, so `genesis.password` is too without restating it.
- **`-` clears only the inherited set.** `genesis.email` is `awareness:"-"` because it is
  reconciled on every boot; `onboard.setupToken` is `awareness:"-,secret"` — not bootstrap,
  still a secret. `genesis_awareness_test.go` pins those two opt-outs.

## Modes

`mode` is `selfhosted` (default) or `cloud`. It selects which identity mechanisms exist. It
never changes a wire contract.

| Property                 | `selfhosted`                                     | `cloud`                                 |
| ------------------------ | ------------------------------------------------ | --------------------------------------- |
| `db.driver`              | `sqlite` or `postgres`                           | `postgres` only                         |
| `oidc.*`                 | optional                                         | `issuer` + `clientID` + `clientSecret`  |
| `genesis.email`          | optional                                         | required                                |
| `security.encryptionKey` | required under `postgres`                        | always required                         |
| `tenant.singletonOrg.*`  | optional                                         | `name` required; `slug` optional        |
| Local password form      | shown once a local user exists, or genesis creds | hidden unless `genesis.breakGlass=true` |
| Org creation from UI     | disabled                                         | enabled                                 |
| Public signup            | disabled                                         | enabled (needs OIDC)                    |

The keyed rows are boot-time invariants in `config.Validate()`. The last three are flags derived
once in `internal/platform/capabilities/capabilities.go`, so no page re-decides what a mode
means. Tenant model: [`multitenancy`](../multitenancy/README.md).

## First boot

The first admin arrives by identity mechanism, not by mode: under OIDC `genesis.email` is a
standing claim applied on the first matching login (reversible until claimed); with a local
password a human consents at `/onboard`; unattended, `genesis.email` + `genesis.password` seed
one account once and never overwrite it. Boot writes nothing — it reconciles the claim:

| `genesis.email`     | Boot does                                                     |
| ------------------- | ------------------------------------------------------------- |
| matches an admin    | nothing — satisfied, silent                                   |
| matches a non-admin | promotes; logs `bootstrap: genesis admin promoted`            |
| matches nobody      | logs `bootstrap: genesis admin not yet claimed` on every boot |
| empty               | nothing to reconcile                                          |

- Re-evaluated every boot, so a typo in `genesis.email` is fixed by fixing the env var. An
  already-promoted admin keeps `is_admin` — demote it in the app.
- `/onboard` or `opensheet init` (never boot: `orgs.created_by` needs a user) creates the first org,
  owner and bootstrap row. Seeds: `tenant.singletonOrg.name` (`Default Organization`);
  `tenant.singletonOrg.slug` and `tenant.personalProjectSlug` default to `""`: a slug is generated
  (`/onboard` shows it read-only with Edit; cloud signup's first project too); a set value wins.
  Logins find the org by `orgs.system`, not slug; a unique index allows one system org, and a
  second onboarding joins it. **Upgrade:** set both keys to `default` to keep the old slugs.
- **`/onboard` is gated by a one-time setup token.** With `onboard.setupToken` unset, boot mints
  one and logs `boot: setup required — open this one-time onboarding URL url=…/onboard?token=…`.
  Pin it with `OPENSHEET_ONBOARD_SETUP_TOKEN` for automated installs; a pinned token is never echoed.
- **Cloud + a genesis password requires `genesis.breakGlass=true`.** Without it boot fails loud:
  the local login form is hidden in cloud, so the genesis user would be unreachable via the UI.

## Identity and tokens

| Key                                 | Default                 | Awareness                         | Meaning                                                                                   |
| ----------------------------------- | ----------------------- | --------------------------------- | ----------------------------------------------------------------------------------------- |
| `oidc.issuer`                       | `""`                    | `required, mode:cloud`            | The Othra project issuer (`https://<othra>/p/<handle>`). Enables the OIDC button.         |
| `oidc.clientID`                     | `""`                    | `required, mode:cloud`            | Confidential Othra client for the web flow.                                               |
| `oidc.clientSecret`                 | `""`                    | `required, mode:cloud, secret`    | Paired with `clientID`.                                                                   |
| `oidc.cliClientID`                  | `""`                    | `-`                               | Native public Othra client for `auth login`; the API's RFC 9728 document names it.        |
| `oidc.redirectPort`                 | `0`                     | `-`                               | Loopback port `auth login` binds; `0` picks a free one.                                   |
| `oidc.endSessionOnLogout`           | `true`                  | `-`                               | Signing out also ends the Othra session (RP-Initiated Logout).                            |
| `http.baseURL`                      | `""`                    | `required`                        | Public URL. Both resource identifiers derive from it.                                     |
| `http.stateSecret`                  | `""`                    | `required, secret, bootstrap`     | Signs the sign-in state cookie, the `sid` cookie and the Google connect and picker state. |
| `tokens.issuer`                     | `""`                    | `required, mode:cloud`            | Issuer the bearer verifier trusts; must equal `oidc.issuer`. **MCP needs it always.**     |
| `tokens.audience`                   | `baseURL+basePath+/api` | `required, mode:cloud, bootstrap` | The control plane's Othra resource identifier. Not the MCP audience.                      |
| `tokens.clockSkew`                  | `60s`                   | `-`                               | Leeway on `exp` / `iat` / `nbf`.                                                          |
| `tokens.introspection.clientID`     | `""`                    | `-`                               | The API resource server's public id; set it to accept `othra_pt_` personal tokens.        |
| `tokens.introspection.clientSecret` | `""`                    | `secret`                          | Its introspection secret (`othra_rs_…`).                                                  |

- Signing algorithms come from the issuer's discovery document (RS256 for Othra); an access
  token must carry `typ: at+jwt`, so an ID token is never accepted as one.
- Each resource serves its RFC 9728 document at `/.well-known/oauth-protected-resource{basePath}/api`
  and `…/mcp`; Othra reads it before it mints tokens for that resource.
- Personal tokens are introspected on every call, never cached, so a revoked one stops at once.
- `cloud` refuses to boot without `oidc.clientID` and `oidc.clientSecret`, yet Othra registers a
  resource server only after reading its RFC 9728 document. Boot with placeholders first; the
  registration order is in [`deployment`](../deployment/README.md#oidc).
- `cloud` needs `db.driver=postgres`; a local recipe is in
  [`deployment`](../deployment/README.md#cloud-mode-locally).

## Machine surfaces

| Key                 | Default | Awareness | Meaning                                                                                                      |
| ------------------- | ------- | --------- | ------------------------------------------------------------------------------------------------------------ |
| `api.enabled`       | `true`  | `-`       | Mounts the control plane at `/api/`. Off means no Connect-RPC at all — the CLI's remote mode stops working.  |
| `api.keyPrefix`     | `key_`  | `-`       | Literal prefix every minted API key carries. It is how `authn.Scheme` tells a key from a JWT by shape alone. |
| `dataplane.enabled` | `true`  | `-`       | Mounts the REST data plane at `/api/v1/`.                                                                    |
| `blog.publicReads`  | `false` | `-`       | Lets an uncredentialed caller read **published** posts over the data plane.                                  |

- **Changing `api.keyPrefix` after keys exist orphans them.** The shape check runs before any
  lookup, so a key minted under the old prefix fails as an unknown credential shape, not as a bad
  key. Bootstrap-in-practice: pick it before the first key is minted.
- `blog.publicReads` is R9 in [`surfaces`](../surfaces/README.md). SECURITY: it gates **reads of
  published rows only**. A draft is a 404 either way — never a 403, because confirming a slug
  exists is itself a disclosure — and every write route requires a key regardless of the flag.
  Scope strings a key or token can carry: [`scopes`](../scopes/README.md).

## MCP and queue

- `mcp.enabled` mounts S7 at `basePath + /mcp`; boot fails without `tokens.issuer` and without
  `http.baseURL` or `mcp.audience`. Keys, defaults, setup order, Apps UI: [`mcp`](../mcp/README.md).
- `queue.*` is the NATS connection behind `Submit` and `Emit`: [`queue`](../queue/README.md#config).

## Database

| Key                         | Default                     | Awareness            | Meaning                                                                                       |
| --------------------------- | --------------------------- | -------------------- | --------------------------------------------------------------------------------------------- |
| `db.driver`                 | `sqlite`                    | `required,bootstrap` | `sqlite` or `postgres`.                                                                       |
| `db.dsn`                    | `~/.opensheet/opensheet.db` | `required,secret`    | The runtime connection. In production this is the `_service` role.                            |
| `db.autoMigrate`            | `true`                      | `-`                  | Apply pending migrations at boot.                                                             |
| `db.schema`                 | `public`                    | `bootstrap`          | Postgres schema every object is created in.                                                   |
| `db.tablePrefix`            | `opensheet_`                | `bootstrap`          | Prefix on every table name.                                                                   |
| `db.role`                   | `""`                        | `bootstrap`          | `SET ROLE` issued on each **runtime** connection. Never reaches migrations.                   |
| `db.migrator.dsn`           | `""`                        | `secret,bootstrap`   | Separate credential used only for migrations, then closed.                                    |
| `db.migrator.role`          | `""`                        | `bootstrap`          | Sole source of the migration role — `SET ROLE` once per migration connection.                 |
| `db.reader.dsn`             | `""`                        | `secret`             | Replica for non-tenant reads. Empty aliases the writer; ignored under `sqlite`.               |
| `db.allowBypassRLS`         | `false`                     | `bootstrap`          | Skips `schema.RLSGuard`. Dev only — `true` in production means no tenant isolation.           |
| `db.connectTimeout`         | `30s`                       | `-`                  | Total budget for the initial connect-and-ping, retries included. `0` disables retrying.       |
| `db.connectBackoff`         | `250ms`                     | `-`                  | Starting backoff between connect attempts; doubles, capped at 5s, never overruns the timeout. |
| `db.health.interval`        | `30s`                       | `-`                  | Tick cadence of the `db-health` worker; how stale a `/readyz` answer can be.                  |
| `db.health.timeout`         | `2s`                        | `-`                  | Per-handle ping timeout within one probe round.                                               |
| `tenant.rlsEnforce`         | `true`                      | `bootstrap`          | Renders the RLS clauses into the Postgres migration templates.                                |
| `tenant.tenantScopedTables` | `[]`                        | `bootstrap`          | Extra tables `schema.RLSGuard` audits beyond the generated list.                              |

Boot fails if the runtime role holds `BYPASSRLS` and `db.allowBypassRLS` is `false`
(`schema.RLSGuard`, `ErrRLSBypass`). Role graph and grants:
[`multitenancy`](../multitenancy/README.md#postgres-roles). DSN wiring and probes:
[`deployment`](../deployment/README.md#postgres).

## Scheduler

| Key                              | Default | Awareness   | Meaning                                                                                                                  |
| -------------------------------- | ------- | ----------- | ------------------------------------------------------------------------------------------------------------------------ |
| `scheduler.enabled`              | `true`  | `bootstrap` | Master switch for the periodic-job runner. `false` boots the app with no jobs; `opensheet scheduler run` then exits `7`. |
| `scheduler.timezone`             | `UTC`   | `bootstrap` | IANA zone for every wall-clock schedule. Rejected at boot if `time.LoadLocation` cannot resolve it.                      |
| `scheduler.shutdownGrace`        | `30s`   | `-`         | How long the runner waits for in-flight jobs on shutdown before giving up.                                               |
| `scheduler.jobs.<name>.timezone` | —       | `-`         | Per-job override, keyed by the job name from `opensheet scheduler list`.                                                 |

- Timezone resolves as `scheduler.jobs.<name>.timezone`, then `scheduler.timezone`, then UTC.
  Overrides affect **wall-clock** schedules only (cron, daily-at). Boot warns when an override
  names an unknown job, or a job on an interval schedule — an interval has no anchor to shift.
- **Job cadences are deliberately not configurable.** A cadence is a package constant in the
  owning module's `scheduler.go` (e.g. `sweepCron` in `internal/todo/scheduler.go`): changing one
  changes the domain's behavior and belongs in review, not in a deploy-time env var.

## Encryption at rest

| Key                      | Default | Awareness                     | Meaning                                                                         |
| ------------------------ | ------- | ----------------------------- | ------------------------------------------------------------------------------- |
| `security.encryptionKey` | `""`    | `required, secret, bootstrap` | 32 bytes as hex or standard base64. Seals sessions and webhook signing secrets. |

Web sessions are rows in `sessions`; the payload holds the `Principal`, which carries a live IdP
ID token. The row is sealed with AES-256-GCM bound to its session id, so a row copied under
another id will not open. The table carries no `org_id` and no RLS policy: a session is resolved
before any tenant scope exists, so a policy on it would reject every login under
`db.allowBypassRLS=false`.

`db.driver=postgres` and `mode=cloud` both require the key. Under `sqlite` boot mints an **ephemeral** one
and warns `security.encryptionKey is empty — using an ephemeral key; sessions and webhook signing secrets
will not survive a restart; …`. After a restart or a key rotation nothing old opens: users are signed out
(`session-sweep` reaps the rows) and deliveries fail with `webhook.SecretUnavailableError` until the tenant
rotates the endpoint secret. The endpoint page shows a banner (`capabilities.EphemeralEncryptionKey`).

## Validation

`config.Validate()` runs on every boot with specific messages (e.g.
`mode=cloud requires db.driver=postgres, got "sqlite"`). MCP's four failures are typed:
`MCPIssuerRequiredError`, `MCPBaseURLRequiredError`, `MCPAudienceInvalidError`,
`MCPAudienceMismatchError`.

Boot additionally asserts that every table in `schema.RequiredTableSuffixes` exists, failing with
`schema: missing table(s) …`. Goose records only a version number and never checksums, so editing
an already-applied migration is a silent no-op — this guard turns that mystery into a message.
