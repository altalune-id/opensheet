# opensheet deployment

The template page is [`deployment`](README.md); workers and the `serve` flag matrix are in
[`workers`](workers.md). This page is what opensheet adds.

## Required secrets under Postgres

`security.encryptionKey` and `http.stateSecret` are both required under `db.driver=postgres`, in
every mode. See [`config/opensheet.md`](../config/opensheet.md#rules-opensheet-adds).

## Session sweep without the scheduler

NOTE: a Postgres deployment running with `scheduler.enabled=false` never runs `session-sweep`, so
it needs its own periodic `DELETE FROM <prefix>sessions WHERE expires_at < now()`.
