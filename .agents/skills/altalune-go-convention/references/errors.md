# Typed errors and error codes

Raising an error from any layer is [`howto/errors.md`](../../../../docs/howto/errors.md).
Registering a new `<DOM><NNN>` is
[`howto/error-code.md`](../../../../docs/howto/error-code.md). The struct shape, the
`Is<FullTypeName>` naming rule and the `"<module>: <situation>: <cause>"` `Error()` format are in
[`modules`](../../../../docs/modules/README.md#2-inside-a-module). The registry is
[`error codes`](../../../../docs/errors/README.md).

This file is the judgement calls those do not make for you.

## Which failures get a type

Expected ones — anything a caller might reasonably branch on, or a user might see: not found,
already exists, invalid input, in use, forbidden transition, stale version.

Unexpected ones go through `s.unexpected(ctx, "<name>.<Method>: <situation>", err, k, v...)` and
surface as the generic code. Do not invent a typed error for a driver failure.

**A refused conditional write needs two types, not one.** `StaleVersionError{Want, Got}` maps to
`codes.FailedPrecondition` so the client can re-read and retry; `NotFoundError` maps to
`codes.NotFound`. Collapsing them tells a client to retry a row that will never exist. The
adapter cannot tell them apart from `RowsAffected() == 0` alone — see
[`persistence.md`](persistence.md#optimistic-concurrency).

## Picking a mnemonic

Three letters naming **what the user was doing**, not the package topology — the code appears on
the user's screen and they quote it back. A module with subdomains is usually clearer with one
mnemonic per subdomain (`BLG`, `CAT`, `TAG`) than one shared block.

## After editing the markdown

Run `pnpm exec prettier --write ../../../../docs/errors/README.md`. lint-staged checks markdown formatting and
a hand-aligned table will not match.

## Constraint violations

Adapters translate driver errors and never leak a `*pgconn.PgError` or a SQLite error. The exact
codes per driver — including the non-obvious one, where `ON DELETE RESTRICT` reports a different
SQLite errcode than an insert-side FK violation — are in
[`persistence.md`](persistence.md#constraint-translation).
