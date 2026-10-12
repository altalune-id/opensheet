# Queue: opensheet

opensheet's additions to the [queue contract](README.md). No jobs yet; one broadcast.

## `sheet.snapshot_invalidated` v1

Subject `broadcast.sheet.snapshot_invalidated.v1`. Declared in `internal/sheet/broadcast.go`;
the listener is `sheet.SnapshotListener`, registered by `opensheetListenerProviders` in
`internal/boot/surfaces_opensheet.go`.

| Field      | Type   | Meaning                                                                          |
| ---------- | ------ | -------------------------------------------------------------------------------- |
| `sheet_id` | UUID   | The sheet whose cached snapshots changed                                         |
| `etag`     | string | Optional. The ETag of the snapshot the emitter now holds; an entry with it stays |

The org, project and user travel in the tenant headers, not the body.

- **Emitted** after every change that drops or rewrites a sheet's snapshots, once the change
  commits: `Service.PurgeCache`, `Update` and `Delete`, and every `WriteWorkflow` write (`Append`,
  `PatchRow`, `CreateRow`, `CreateRows`, `ReplaceRow`, `SoftDeleteRow`). A write that purges sends
  no `etag`; a write-through that stored a rebuilt snapshot sends its ETag, so the emitter keeps it
  when it hears itself. A failed change emits nothing. An `Emit` error is reported and the change
  still succeeds.
- **Listener** drops the sheet's entries from this replica's `memory` snapshot store, except an
  entry whose ETag equals `etag`. With the `postgres` driver the cache is shared, so it does
  nothing. Idempotent.
- **A missed broadcast is harmless.** The memory cache is rebuilt from Google on demand, so a
  replica that misses one serves the stale snapshot only until its TTL, as it did before the
  broadcast existed.
- Not emitted by reads that refill the cache, nor by `FixWorkflow.AddIDColumn`, which writes to
  Google without touching any snapshot.

## When to enable

Several replicas on `cache.driver=memory` (the `auto` default on sqlite) need `queue.enabled=true`,
or a write on one replica leaves the others serving the old snapshot until `cache.defaultTTL` (or
the sheet's own TTL) runs out. One replica, or `cache.driver=postgres`, needs nothing.
