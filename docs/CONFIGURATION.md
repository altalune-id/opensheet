# Configuration

## Precedence

`defaults <- config.yaml <- ALT_* env`. Env always wins. Nested keys map
by dot notation with dots → underscores:

| YAML                               | Env var                               |
| ---------------------------------- | ------------------------------------- |
| `mode: cloud`                      | `ALT_MODE=cloud`                      |
| `db.driver: postgres`              | `ALT_DB_DRIVER=postgres`              |
| `http.basePath: /altempl`          | `ALT_HTTP_BASE_PATH=/altempl`         |
| `tokens.audience: urn:altempl:api` | `ALT_TOKENS_AUDIENCE=urn:altempl:api` |
| `tenant.singletonOrg.slug: main`   | `ALT_TENANT_SINGLETON_ORG_SLUG=main`  |

`config.example.yaml` and `.env.example` are generated from struct tags
in `internal/platform/config`. After editing those, run `make config-examples`.

## Awareness markers

Every `.env.example` field carries a marker in `[brackets]`:

| Marker                           | Meaning                                                             |
| -------------------------------- | ------------------------------------------------------------------- |
| `required`                       | boot fails if unset (in the applicable mode)                        |
| `bootstrap`                      | locks in at first boot; changing later is a no-op on persisted data |
| `secret`                         | never commit; `secret` fields never emit defaults                   |
| `mode:cloud` / `mode:selfhosted` | only meaningful in the named mode                                   |

## Modes

| Property             | `selfhosted`           | `cloud`                                              |
| -------------------- | ---------------------- | ---------------------------------------------------- |
| DB driver            | `sqlite` or `postgres` | `postgres` only                                      |
| OIDC identity        | optional               | required (`issuer` + `clientID` + `clientSecret`)    |
| Local password login | on by default          | off; set `ALT_GENESIS_BREAK_GLASS=true` to re-enable |
| Org creation from UI | disabled               | enabled                                              |
| Public signup        | disabled               | enabled                                              |

Onboarding and admin bootstrap differ by _identity mechanism_, not by mode.
Mode only decides which mechanisms are enabled.

| Identity          | How the first admin is established                                   | Reversible by config?  |
| ----------------- | -------------------------------------------------------------------- | ---------------------- |
| OIDC              | `genesis.email` is a standing claim, applied on first matching login | Yes, until claimed     |
| Local password    | The `/onboard` form — a human types it and consents                  | N/A, human-driven      |
| Local, unattended | `genesis.email` + `genesis.password`, seeded once, never overwritten | No, one-shot by design |

## First-boot

Boot writes nothing. It reconciles the genesis claim and reports:

```
bootstrap row exists?      → skip; boot into dashboard
genesis.email matches an admin?     → satisfied, silent
genesis.email matches a non-admin?  → promote, log "genesis admin promoted"
genesis.email matches nobody?       → log a warning every boot until claimed
genesis.email empty?                → nothing to reconcile
```

The first org, its owner membership and the bootstrap row are created on
first login or through `/onboard` — not at boot. `orgs.created_by` is
`NOT NULL REFERENCES users(id)`, so there is no org to create until a user
exists. Seeds use `ALT_TENANT_SINGLETON_ORG_SLUG` (default `default`),
`ALT_TENANT_SINGLETON_ORG_NAME` (default `Default Organization`), and
`ALT_TENANT_PERSONAL_PROJECT_SLUG` (default `default`).

**Changing `genesis.email` after boot** takes effect on the next boot: the
claim is re-evaluated every time, so a typo is corrected by fixing the env
var. A previously claimed admin keeps its `is_admin` flag — demote it in the
app. This is why `genesis.email` is not a `bootstrap` value.

**The `/onboard` form** is gated by a one-time setup token. When
`onboard.setupToken` is unset, boot mints one and logs the ready-to-use URL:

```
boot: setup required — open this one-time onboarding URL url=https://host/onboard?token=<token>
```

Pin it with `ALT_ONBOARD_SETUP_TOKEN` for automated installs; a pinned token
is never echoed to the logs. Local path (email + password + org + project →
dashboard) is available when `caps.LocalIdentity` is on; OIDC path when
`caps.ExternalIdentity` is on. Cloud shows only OIDC by default; selfhosted
shows both.

**Cloud + genesis + break-glass** — setting `ALT_GENESIS_EMAIL` +
`ALT_GENESIS_PASSWORD` in cloud requires `ALT_GENESIS_BREAK_GLASS=true`.
Without it, boot fails loud: the local login form is hidden in cloud, so
the genesis user would be unreachable via the UI.

## Scheduler

| Key                              | Default | Awareness   | Meaning                                                                                                                |
| -------------------------------- | ------- | ----------- | ---------------------------------------------------------------------------------------------------------------------- |
| `scheduler.enabled`              | `true`  | `bootstrap` | Master switch for the periodic-job runner. `false` boots the app with no jobs; `altempl scheduler run` then exits `7`. |
| `scheduler.timezone`             | `UTC`   | `bootstrap` | IANA zone for every wall-clock schedule. Rejected at boot if `time.LoadLocation` cannot resolve it.                    |
| `scheduler.shutdownGrace`        | `30s`   | `-`         | How long the runner waits for in-flight jobs on shutdown before giving up.                                             |
| `scheduler.jobs.<name>.timezone` | —       | `-`         | Per-job override of `scheduler.timezone`, keyed by the job name from `altempl scheduler list`.                         |

Timezone resolves in three steps: `scheduler.jobs.<name>.timezone`, then
`scheduler.timezone`, then UTC.

Overrides only affect **wall-clock** schedules (cron, daily-at). Boot logs a
warning when an override names an unknown job, or a job on an interval
schedule — an interval has no wall-clock anchor to shift.

**Job cadences are deliberately not configurable.** A cadence is a package
constant in the owning module's `scheduler.go` (e.g. `sweepCron` in
`internal/todo/scheduler.go`), because changing one changes the domain's
behavior and belongs in review, not in a deploy-time env var. Only the
timezone is an operator knob.

## Database

| Key                  | Default | Awareness | Meaning                                                                                                            |
| -------------------- | ------- | --------- | ------------------------------------------------------------------------------------------------------------------ |
| `db.connectTimeout`  | `30s`   | `-`       | Total budget for the initial connect-and-ping at boot, retries included. `0` disables retrying.                    |
| `db.connectBackoff`  | `250ms` | `-`       | Starting backoff between connect attempts; doubles per attempt, capped at 5s, and never overruns `connectTimeout`. |
| `db.health.interval` | `30s`   | `-`       | Tick cadence of the `db-health` worker.                                                                            |
| `db.health.timeout`  | `2s`    | `-`       | Per-handle ping timeout within one probe round.                                                                    |

The separate reader handle is Postgres-only; under `driver: sqlite` its DSN is
ignored and the reader aliases the writer.

`/readyz` reports the snapshot the `db-health` worker writes, so
`db.health.interval` sets how stale a readiness answer can be. Boot takes one
synchronous probe, so `/readyz` is accurate before the first tick. The worker
is independent of the scheduler and runs in every replica, so `/readyz` stays
DB-aware under `scheduler.enabled=false` and `serve --no-scheduler` alike. See
[`DEPLOYMENT.md`](DEPLOYMENT.md) for the role grants and the RLS interaction.

## Validation

`config.Validate()` runs on every boot with specific messages
(e.g. `cloud requires db.driver=postgres, got "sqlite"`).
