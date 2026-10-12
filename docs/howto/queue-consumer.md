# Add a queue job or broadcast

Internal work over NATS, not a surface: no route, no credential. The contract you are extending — names, retries, the DLQ, the caller rule, flags: [`queue`](../queue/README.md). Reference: `todo.log_completion` in `internal/todo/consumer.go`.

Pick the verb first:

| I want                                                                       | Verb                                       |
| ---------------------------------------------------------------------------- | ------------------------------------------ |
| work done once, by one instance, retried, dead-lettered                      | `Submit` a **job**                         |
| every running instance to refresh state it also rebuilds from the DB at boot | `Emit` a **broadcast**                     |
| to tell a tenant something happened                                          | neither: a [webhook event](webhook-out.md) |

A job never goes in `internal/platform/events`; a tenant-facing fact never goes on the queue.

## Add a job

1. **Job and payload** in the handling module's `consumer.go`, next to the handler:
   - an unexported func, `func sendDigestJob() queue.Job { return queue.Job{Name: "<domain>.<verb_object>", Version: 1} }` (a func, because `gochecknoglobals` forbids a var);
   - an unexported payload struct `<job>V1` with `snake_case` JSON tags.
2. **Handler** in the same file. The module's `Consumer` implements `queue.Provider`; add one `queue.Handler` to `ConsumerHandlers()`:
   - `queue.Decode[<job>V1](m)`; a decode error returns `queue.Permanent(err)`;
   - then call **one** `Service` method and return its error. No business logic in `consumer.go`;
   - optional: `OnDeadLetter` for cleanup, `SuppressReport: true` when a dead letter is expected.
3. **Service method** the handler calls. It must be idempotent: a message can arrive twice. Use `m.ID` as the dedupe key if a side effect needs one. A row deleted since the submit returns nil.
4. **Producer.** The service declares a port in `service.go` (`type Queue interface { Submit(ctx context.Context, j queue.Job, data any) error }`) and takes it in `NewService`; boot passes `Kernel.Queue`. Call `Submit` **after** the write commits, only on the real transition:

   ```go
   if err := s.queue.Submit(ctx, sendDigestJob(), sendDigestV1{…}); err != nil {
       _ = s.unexpected(ctx, "<module>.<Method>: submit", err, "<entity>_id", id)
   }
   return t, nil
   ```

   Report and **still succeed**: the user's change did save. Never `Submit` inside a unit of work.

5. **Boot.** A new module adds its name to `consumerDomains` and its `NewConsumer(...)` to `consumerProviders` in `internal/boot/consumers.go`, in the same order. A module already listed needs nothing: `Declare` reads its handlers.
6. **Tests:**
   - the per-module guard: every job the service submits appears in `ConsumerHandlers()`;
   - service tests on `fakes.Queue` (`internal/testutil/fakes/queue.go`): submitted exactly once on the transition, not on a no-op, and a `Submit` error still returns success and reports;
   - the handler: call `ConsumerHandlers()[i].Handle` with a `queue.Message`; a bad payload is `queue.IsPermanentError`;
   - end to end, an embedded server: `server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})` from `github.com/nats-io/nats-server/v2/server`. Copy `startNATSServer` in `internal/platform/queue/natstest_test.go`. No container, no `time.Sleep`.

**Changing a payload later.** An added field needs nothing. A renamed or retyped field is a new job with `Version: 2` and a new handler; keep the v1 handler until the v1 backlog is empty (at most 7 days).

## Add a broadcast

Only for per-instance, in-memory state that the instance **also** rebuilds from the DB at boot. A missed broadcast must be harmless. Reference: the onboarding gate, `internal/boot/onboarding.go`.

1. **Broadcast and payload** next to the state it refreshes: `func <name>() queue.Broadcast { return queue.Broadcast{Name: "<domain>.<fact>", Version: 1} }` and a `<name>V1` struct.
2. **A `Listeners()` provider** on the type that owns the state (`queue.ListenerProvider`). Each `queue.Listener` updates that state and returns. It must be **idempotent**: the emitter hears its own broadcast.
3. **Emit** from the same type, after the commit, with the caller rule above: update local state first, then `Emit`, and report an error without failing.
4. **Declare it.** Add the provider to `listenerProviders` in `internal/boot/consumers.go`, one line. Boot passes its broadcasts to `Declare` and its listeners to `queue.NewListener`. An undeclared broadcast is `*UndeclaredBroadcastError`.
5. **Tests:** two clients on one embedded server, each with its own listener; `Emit` on one flips both. Revert the `Emit` call and watch the test fail.

## Tenancy

- **The scope travels in headers.** `Submit` copies org, project and user from ctx; the consumer binds them with `tenant.Into` before `Handle`, so tenant-scoped stores work unchanged.
- **The handler checks org and project after loading**, like any `Service` method taking a bare id.
- **Unscoped work** submits from a ctx with no tenant; the handler then runs with no scope and must not touch a tenant-scoped store.
- SECURITY: the headers are trusted. Keep NATS on the private network with a token ([`deployment/workers.md`](../deployment/workers.md#queue)).

## Gotchas

- **No job without a handler.** An undeclared `Submit` fails at once; the per-module test catches it before merge.
- **`queue.enabled=false` in dev and tests** makes `Submit` a no-op, so a producer test on the real client proves nothing. Use `fakes.Queue` or the embedded server.
- **Slow work.** `HandlerTimeout` is 25s. Longer work is split into jobs.
- **No replay tool yet.** A dead letter stays in `DLQ` for 30 days; inspect it with the `nats` CLI.

## Contracts

[`queue`](../queue/README.md) · [`modules`](../modules/README.md) · [`platform`](../platform/README.md) · [`webhooks`](../webhooks/README.md#versioning) · [`request scope`](../multitenancy/request-scope.md)
