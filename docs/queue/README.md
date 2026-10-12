# Queue

Internal work over NATS JetStream, in `internal/platform/queue`. This page is the contract.
Adding a job or a broadcast: [`howto/queue-consumer.md`](../howto/queue-consumer.md). Running
NATS: [`deployment/workers.md`](../deployment/workers.md#queue). NATS is internal
infrastructure, not a surface: no route, no credential class.

## Two verbs

| Verb                         | Means                          | Who runs the handler                         |
| ---------------------------- | ------------------------------ | -------------------------------------------- |
| `Submit(ctx, job, data)`     | work that must happen once     | exactly one instance, with retries and a DLQ |
| `Emit(ctx, broadcast, data)` | "refresh your in-memory state" | every running instance, once, with no retry  |

**A job is not a webhook event.**

- A job is internal work, handled by our code in this repo. It is declared by the domain that
  handles it, in its `consumer.go`, and never goes in `internal/platform/events`.
- A webhook event is a tenant-facing fact and a public contract
  ([`webhooks`](../webhooks/README.md#versioning)). It never goes on the queue.
- One action that needs both makes two calls with two separate types. A name may appear on both
  paths without meaning the same thing.

## Names

| Thing             | Rule                                                             | Example                                    |
| ----------------- | ---------------------------------------------------------------- | ------------------------------------------ |
| Job name          | `<domain>.<verb_object>`, matching `^[a-z0-9_]+(\.[a-z0-9_]+)+$` | `todo.log_completion`                      |
| Version           | integer, at least 1                                              | `1`                                        |
| Job subject       | `jobs.<name>.v<version>`                                         | `jobs.todo.log_completion.v1`              |
| DLQ subject       | `dlq.` + job subject                                             | `dlq.jobs.todo.log_completion.v1`          |
| Broadcast subject | same name rule; `broadcast.<name>.v<version>`                    | `broadcast.system.onboarding_completed.v1` |
| Durable consumer  | job subject without `jobs.`, `.` → `_`                           | `todo_log_completion_v1`                   |

- **Bump `Version`** when an in-flight message would not decode: a renamed or retyped field. Keep
  the old handler until the old subject's backlog is empty, at most 7 days. An added field needs
  no bump. Jobs have no golden files: nothing outside the repo sees them.
- **No app prefix.** One NATS server or account per deployment. Two apps on one server are
  isolated with NATS accounts, never subject prefixes.

## Streams

Created or updated on every `Connect` (`CreateOrUpdateStream`, idempotent). File storage, 1
replica, `Duplicates 2m` on all three. The sizes are package constants, not config.

| Stream      | Subjects      | Retention   | `MaxAge` | `MaxBytes` | Discard                             |
| ----------- | ------------- | ----------- | -------- | ---------- | ----------------------------------- |
| `WORK`      | `jobs.>`      | `WorkQueue` | 7d       | 256 MiB    | `New`: a full stream refuses Submit |
| `DLQ`       | `dlq.jobs.>`  | `Limits`    | 30d      | 256 MiB    | `Old`                               |
| `BROADCAST` | `broadcast.>` | `Limits`    | 1h       | 64 MiB     | `Old`                               |

## Publishing

1. **Declared only.** Boot calls `Declare(jobs, broadcasts)` once, after the services are built
   and before any worker or handler starts, with every job some handler covers. `Submit` refuses
   any other job (`*UndeclaredJobError`), and `Emit` any other broadcast
   (`*UndeclaredBroadcastError`). Every instance declares, even one that does not consume.
2. **Body** is `json.Marshal(data)`. Metadata travels in headers only:

   | Header                                                          | Value                          |
   | --------------------------------------------------------------- | ------------------------------ |
   | `Nats-Msg-Id`                                                   | a new UUIDv7, the message id   |
   | `Opensheet-Job` + `Opensheet-Job-Version`                       | a job's name and version       |
   | `Opensheet-Broadcast` + `Opensheet-Broadcast-Version`           | a broadcast's name and version |
   | `Opensheet-Created-At`                                          | RFC 3339 with nanoseconds      |
   | `Opensheet-Org-Id`, `Opensheet-Project-Id`, `Opensheet-User-Id` | each only when set on ctx      |
   | `traceparent`, `tracestate`                                     | W3C trace context              |

3. **Budget.** Publish runs on `context.WithoutCancel(ctx)` with 5s in total, 1s per attempt and
   200ms between attempts. It retries a timeout, no responders and a lost connection. It does not
   retry a full stream or an oversized message. A duplicate ack counts as success.
4. **Failure** returns `*PublishError{Subject, Cause}`. `Submit` and `Emit` never report.

**Caller rule.** Call `Submit` or `Emit` **after** the business write commits: NATS cannot join a
DB transaction. On an error, report it with `s.unexpected(ctx, "<module>.<Method>: submit", err, …)`
and **still return success**, because the change did save. Work is lost only when NATS is
unreachable for the whole 5s, and that loss is always reported.

## Consuming a job

One durable pull consumer per job: `AckExplicit`, `AckWait` 30s, `MaxDeliver` unlimited,
`MaxAckPending` 16 across all instances, one message per pull, 15s heartbeat. The handler ctx is
`WithoutCancel` plus `HandlerTimeout` (25s), with trace and tenant bound. A panic is an error.

`n` is the delivery count. `MaxAttempts` is 5.

| Delivery | Handler returns   | Outcome                                                                 |
| -------- | ----------------- | ----------------------------------------------------------------------- |
| `n > 5`  | not run           | dead letter, reason `exhausted` ("outcome unknown: redelivered past …") |
| any      | nil               | ack                                                                     |
| any      | `queue.Permanent` | dead letter, reason `permanent`                                         |
| `n < 5`  | an error          | nak with delay: 10s, 1m, 5m, 15m after attempts 1 to 4                  |
| `n = 5`  | an error          | dead letter, reason `exhausted`                                         |

A malformed id, version or tenant header is `permanent` before the handler runs.

**Dead letter.** Publish to `dlq.<subject>` within `DLQPublishTimeout` (4s): the same body and
headers except `traceparent`, which points at the DLQ publish span when tracing is on, the
same `Nats-Msg-Id`, plus `Opensheet-Dlq-Reason`, `Opensheet-Dlq-Error` (at most 1 KiB, valid
UTF-8), `Opensheet-Dlq-Attempts` and `Opensheet-Dlq-Stream-Seq`.

| DLQ publish | Then                                                                                                                       |
| ----------- | -------------------------------------------------------------------------------------------------------------------------- |
| accepted    | term; `OnDeadLetter` if set (its error is logged); report `queue: dead-lettered` unless `SuppressReport` (then a Warn log) |
| failed      | nak with 1m delay; report `queue: dlq publish failed`, always; `queue.dlq_publish_failed`                                  |

- **A failed DLQ publish never loses the message.** It stays in `WORK` and returns in a minute,
  reported on every attempt until the DLQ accepts or `WORK`'s 7d `MaxAge` passes. An
  **exhausted** one arrives with `n > 5` and goes straight to the dead-letter step without running
  the handler. A **permanent** one runs the handler **again**, because the nak cannot carry the
  verdict (at-least-once allows it), and is re-dead-lettered with its real cause.
- The same path catches a process that dies on the last attempt. That is why the server-side
  `MaxDeliver` is unlimited: with a server cap the message would stay in `WORK` with no DLQ entry.
- **SECURITY: the tenant headers are trusted.** A valid `Opensheet-Org-Id` (plus project and user)
  is bound with `tenant.Into` before `Handle`; no org header means system work with no scope.
  Anyone who can publish to `WORK` can act as any tenant, so the NATS credential is as sensitive as
  the DB DSN.
- **At least once.** An ack can be lost and shutdown can cut a handler short. Handlers are
  idempotent; `Message.ID` is the dedupe key when a side effect needs one.
- `Consumer.Run` returns nil on shutdown (drains for up to 8s). A consume loop that closes, or a
  durable found gone after a missed heartbeat, returns `*ConsumerClosedError`: the process stops
  and the restart recreates streams and consumers.

## Broadcasts

- **`Emit` refreshes state; it never does work.** A broadcast can be missed (the instance is down,
  NATS is unreachable), so a listener only changes state the instance **also** rebuilds from the
  DB at boot. Anything that must happen is a job.
- **One ordered consumer per instance** over every listened subject. It starts at
  `BroadcastStartSeq`, which boot reads right after `Connect` and before it reads the DB, so a
  broadcast sent in between is still delivered.
- **The emitter hears itself.** Every listener is idempotent.
- **No ack, no retry, no DLQ.** The listener handler timeout is 5s. A listener error or panic is logged,
  counted (`queue.broadcast_failed`) and reported as `queue: broadcast listener failed`.
- Worker `queue.listener` runs on every instance with `queue.enabled=true`, whatever the flags.
  It returns nil on shutdown and `*ConsumerClosedError{Subject: "broadcast"}` if its loop closes.
- Reference: `system.onboarding_completed` v1 in `internal/boot/onboarding.go` clears every
  replica's onboarding gate.

## Disabled

`queue.enabled=false` (the default) builds `queue.Disabled(log)`, the same `*queue.Client` with
no connection, so `Kernel.Queue` is never nil. Boot logs `queue: disabled — Submit is a no-op`.

- `Submit` and `Emit` return nil and do nothing. `Declare` still validates.
- `NewConsumer` and `NewListener` return `*DisabledError`, so no consumer or listener runs.
- One replica is fine. Several replicas keep a stale onboarding gate until they restart.

## Config

| Key                    | Default | Awareness | Meaning                                                         |
| ---------------------- | ------- | --------- | --------------------------------------------------------------- |
| `queue.enabled`        | `false` | `-`       | Master switch. Off: no connection, no-op verbs, no consumer     |
| `queue.url`            | `""`    | `secret`  | e.g. `nats://nats.railway.internal:4222`. Required when enabled |
| `queue.token`          | `""`    | `secret`  | NATS auth token, for a server without accounts                  |
| `queue.user`           | `""`    | `-`       | NATS account user; set with `queue.password`, never with token  |
| `queue.password`       | `""`    | `secret`  | That user's password                                            |
| `queue.connectTimeout` | `10s`   | `-`       | Boot connect budget; dial retries back off 250ms doubling to 2s |

When the budget runs out boot fails, like the DB. After the first connect the client reconnects
forever.

## `serve` flags

| Flags              | HTTP        | scheduler | consumer | listener | outbox dispatch, db-health |
| ------------------ | ----------- | --------- | -------- | -------- | -------------------------- |
| none               | full        | yes       | yes      | yes      | yes                        |
| `--no-scheduler`   | full        | no        | yes      | yes      | yes                        |
| `--no-consumer`    | full        | yes       | no       | yes      | yes                        |
| `--scheduler-only` | health only | yes       | no       | yes      | yes                        |
| `--consumer-only`  | health only | no        | yes      | yes      | yes                        |

- "consumer yes" and "listener yes" also need `queue.enabled=true`. `--consumer-only` with the
  queue disabled fails boot.
- Mutually exclusive: `--no-consumer` with `--consumer-only`, and `--consumer-only` with
  `--scheduler-only`.

## Telemetry

Spans `queue.publish`, `queue.consume` (parent from `traceparent`) and `queue.listen`. Metrics:
`queue.published`, `queue.publish_failed`, `queue.emitted`, `queue.emit_failed`,
`queue.consumed{outcome=ok|retry|dead_letter}`, `queue.dlq_publish_failed`,
`queue.broadcast_failed`, and the histogram `queue.delivery_attempts`.

## Errors

Each has an `Is<FullTypeName>` helper (`queue.IsPublishError`, …).

| Type                       | When                                                           |
| -------------------------- | -------------------------------------------------------------- |
| `PublishError`             | the stream did not acknowledge a `Submit` or `Emit`            |
| `PermanentError`           | a handler failure retrying cannot fix; built by `Permanent`    |
| `InvalidJobError`          | a job name or version breaks the naming rule                   |
| `InvalidBroadcastError`    | a broadcast name or version breaks the naming rule             |
| `UndeclaredJobError`       | `Submit` of a job no handler was declared for                  |
| `UndeclaredBroadcastError` | `Emit` of a broadcast that was not declared                    |
| `HandlerWiringError`       | two declarations or handlers claim the same subject            |
| `NilHandlerError`          | a `Handler` or `Listener` without a `Handle` func              |
| `ConsumerClosedError`      | a consume or listen loop stopped while the process was running |
| `DisabledError`            | a consumer or listener built on a disabled client              |
