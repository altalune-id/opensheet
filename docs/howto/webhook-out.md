# Add a webhook event

Surface **S5 dispatch** ([`surfaces`](../surfaces/README.md)) — the only egress surface, where we call a URL the tenant controls. Receiving a third party's push is the opposite direction, [`webhook-in.md`](webhook-in.md) (S4). Operator alerts to Slack or Discord are **not** this surface: `internal/platform/notify` is a separate, fire-and-forget code path, and wiring a tenant endpoint into it leaks operational data cross-tenant.

What ships: `internal/platform/outbox/` (table, store, worker, backoff) and `internal/webhook/` (endpoints, console, `Deliverer`). Boot always registers `outbox.NewWorker` with `webhook.NewDeliverer`; `boot.WithDispatch` overrides the deliverer in tests. The receiver contract you are extending: [`webhooks`](../webhooks/README.md).

## Steps

1. **Catalog entry** in `internal/platform/events/catalog.go`:
   - a `Type` const named `<domain>.<noun>.<verb>` (`blog.post.published`);
   - a row in `All()` with `Version: 1`, `Subscribable: true`;
   - a case in `CheckPayload`.
2. **Payload** in `payloads.go`: one named `<Event>V1` type per event, even when two share a shape. JSON `snake_case`, UUIDs, times in UTC, slices never `nil`.
3. **Golden file**: add a `checkGolden` line to `catalog_test.go`, run `go test ./internal/platform/events/ -update`, and review `testdata/<event>_v1.golden.json`. A changed field then fails the test.
4. **Port** in the producing module's `service.go`: `type Webhooks interface { Enqueue(ctx context.Context, t events.Type, data any) error }`. The module imports `internal/platform/events`, never `internal/webhook`. The constructor takes `uow tenant.UnitOfWork` and `hooks Webhooks` after `store, log, unexpected`; boot passes `tenant.NewUnitOfWork(...)` and `*webhook.Service` (`internal/boot/services.go`).
5. **Enqueue inside `s.uow`**: load → mutate → `Save` → `s.hooks.Enqueue(ctx, t, payload)` in one closure, so the outbox rows commit with the write. Reference: `blog.Service.transition` and `blog.Service.Delete`.
   - Emit **only on a real state transition**. A no-op returns early, with no write and no event.
   - An `Enqueue` error goes through `s.unexpected` and rolls the write back.
6. **Console label**: `webhooks.event.<type with "." → "_">` in all five locales (`make i18n-check`). The picker lists `events.Subscribable()` on its own.
7. **Receiver docs**: a row in the Events table of [`webhooks`](../webhooks/README.md#events) and a `data` example.
8. **Tests**:
   - service tests on fakes: the event fires on the transition only, with the right payload;
   - atomicity on SQLite with the **real** store and real unit of work: a failing `Webhooks` fake leaves the row unchanged. Revert the `s.uow` wrapping and watch it fail.

## Tenancy

- **Enqueue reads the scope from ctx.** `webhook.Service.Enqueue` fans out to the active endpoints of `tc.ProjectID` subscribed to the type, one outbox row each. No endpoints means no rows.
- **It needs a transaction on ctx.** Without one it returns `*NoUnitOfWorkError`: each store call would commit alone and a half-done fan-out would lose rows.
- **Fan-out, not inheritance, at delivery.** `Worker.sweep` walks `tenant.Enumerator` — the same `boot.orgEnumerator` the scheduler uses — one org per bound context. That ctx has no `ProjectID`, so the deliverer reads the project from the `Entry` and binds it.
- The enumeration is itself a cross-tenant read, so it cannot go through RLS: `tenant.NewOrgReader` calls the `<prefix>list_org_ids()` `SECURITY DEFINER` wrapper.
- **The scope carries no `UserID`.** A dispatch acts as the system, so anything attributing the event to a person must carry the actor in the payload.
- **Unscoped egress does not exist here.** `Entry.validate` rejects a zero `OrgID` and `ProjectID`. System-wide outbound work belongs on a `scheduler.Job` with `ScopeSystem`, which must not touch a tenant-scoped store.

## Gotchas

- **A webhook payload is a public contract; a queue job is not.** Work that must also notify tenants makes two calls with two types. Versioning rule: [`webhooks`](../webhooks/README.md#versioning).
- **Dedupe.** `UNIQUE (org_id, event_id, target)` makes a repeat `Enqueue` a no-op. Receivers dedupe on `X-Opensheet-Delivery-Id`, one per event per endpoint.
- **At least once.** `ClaimLease` (5 minutes) releases an entry whose dispatcher died, so a receiver can see a delivery twice.
- **Retries.** `outbox.MaxAttempts` is 8 over about 28h (`outbox.Backoff`). A row that exhausts it becomes `StatusFailed`, stays as the delivery log, and the console can requeue it.
- **Transport.** `httpclient.New(...)` refuses private addresses at connect time; `webhook.NewDeliverer` refuses redirects (`CheckRedirect`). `httpclient.WithAllowPrivateHosts(true)` has **no config key**: opening it is an operator decision, never a tenant one.
- **SQLite opens transactions `DEFERRED`.** A read then a write in one unit of work can fail with `SQLITE_BUSY` under a concurrent writer; at dev scale the user retries.
- Run `make test-integration` if you touched `internal/platform/outbox/postgres.go`, `internal/webhook/postgres.go` or their migrations.

## Contracts

[`webhooks`](../webhooks/README.md) · [`surfaces`](../surfaces/README.md) (R10) · [`request scope`](../multitenancy/request-scope.md) · [`modules`](../modules/README.md#3-tenant-scoping) · [`platform`](../platform/README.md)
