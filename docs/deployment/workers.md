# Background workers

Operating the scheduler and the queue across replicas. Parent page: [`deployment`](README.md).
Keys: [`config`](../config/README.md#scheduler) and [`queue`](../queue/README.md#config).

## Scheduler

Jobs run in-process, registered as one `Worker` on the same `Supervisor` as the HTTP listener.
`opensheet scheduler list` prints what is registered; cadence and timezone keys:
[`config`](../config/README.md#scheduler).

**Multiple replicas.** Singleton is per job, not per deployment:

| Job                       | Scope  | Schedule              | Singleton |
| ------------------------- | ------ | --------------------- | --------- |
| `todo-autocomplete-stale` | tenant | cron `0 */6 * * *`    | yes       |
| `session-sweep`           | system | every 1h (±5m jitter) | yes       |

Scale replicas freely. Do not designate a "scheduler replica" for correctness; leader election is
per tick, via `pg_try_advisory_lock` on the writer handle — no migration, no lock table. Under
`driver: sqlite` the locker is a no-op, since there is one writing process.

**Pool sizing caveat.** An advisory lock is session-scoped, so each in-flight singleton job **pins
one writer connection** for its whole run. If `db.maxOpenConns` is capped at all, it must exceed the
number of concurrent singleton jobs, or a job blocks waiting for a connection it can never get while
holding none. `0` (unlimited) is unaffected.

**Deployment shapes.** `serve --no-scheduler` and `serve --scheduler-only` are mutually exclusive
flags. In-process everywhere (the default) costs job load on the latency path; `--no-scheduler` serving
replicas plus one `--scheduler-only` replica costs one more deployment unit. `--scheduler-only` still
binds `http.addr` and still serves `/healthz` and `/readyz` — the `db-health` worker runs there too —
so the same probes work unchanged. Combined with `scheduler.enabled=false` it is rejected at boot: the
process would serve probes and do no work. Readiness is not a factor in the choice; `db-health` is a
worker, not a job.

## Queue

NATS JetStream behind `queue.Submit` and `queue.Emit`. Off by default; the contract, including the
full `serve` flag matrix: [`queue`](../queue/README.md).

**NATS on Railway.** Run [`ghcr.io/altalune-id/nats`](https://github.com/altalune-id/nats): one
server shared by every app, one NATS account per app. Set on every opensheet service:

```bash
OPENSHEET_QUEUE_ENABLED=true
OPENSHEET_QUEUE_URL=nats://nats.railway.internal:4222
OPENSHEET_QUEUE_USER=opensheet
OPENSHEET_QUEUE_PASSWORD=${{nats.OPENSHEET_NATS_PASSWORD}}
```

- SECURITY: connect over the **private network with the account's password**, never the public TCP
  proxy. The tenant headers on a message are trusted, so anyone who can publish can act as any
  tenant: the password is as sensitive as the DB DSN.
- **One account per app, never shared.** Subjects carry no app prefix, so two apps in one account
  consume each other's jobs. Accounts keep same-named streams apart.
- **About 576 MiB of the account's JetStream quota.** The server reserves each stream's `MaxBytes`
  when it creates it: `WORK` 256 + `DLQ` 256 + `BROADCAST` 64 MiB.
- `queue.token` is for a server without accounts. It and `queue.user`/`queue.password` are mutually
  exclusive.
- Boot fails when NATS is unreachable for `queue.connectTimeout` (10s), like the DB. After that
  the client reconnects forever; a `Submit` during an outage longer than 5s is reported, not
  retried later.

**Deployment shapes.** `serve --no-consumer` and `serve --consumer-only` are mutually exclusive,
and `--consumer-only` excludes `--scheduler-only`.

| Shape                                                | Costs                            |
| ---------------------------------------------------- | -------------------------------- |
| in-process everywhere (the default)                  | handler load on the latency path |
| `--no-consumer` web replicas + one `--consumer-only` | one more deployment unit         |

- The durable consumer is shared: any number of consuming replicas split the work, and each job
  runs on one of them. Plain `serve` consumes too, so count it when checking which replica ran
  a job.
- At least one replica must consume, or jobs wait in `WORK` and expire after 7 days.
- `--consumer-only` serves `/healthz` and `/readyz` only, and runs no scheduler. With
  `queue.enabled=false` it fails boot.
- The broadcast listener runs on every replica with `queue.enabled=true`, whatever the flags.

**Shutdown grace.** Set the orchestrator's stop timeout to about **20s** (Railway or k8s
`terminationGracePeriodSeconds`, compose `stop_grace_period`):

- workers get 10s to stop (the `worker.HTTP` budget); the consumer's drain waits at most 8s;
- then `Client.Close` drains the connection for at most 6s. It is normally near-instant, because
  the workers already drained.

**Multiple replicas.** Both hold even for a deployment that submits no jobs:

- **Set `queue.enabled=true`.** The onboarding gate is per-process memory. Only the replica that
  completes `/onboard` clears its own; the others learn through the `system.onboarding_completed`
  broadcast. Without NATS they keep redirecting to `/onboard` until they restart.
- **Pin `OPENSHEET_ONBOARD_SETUP_TOKEN`.** Unset, each replica mints and logs its own token, so the
  `/onboard` URL from one replica's log fails on another.
