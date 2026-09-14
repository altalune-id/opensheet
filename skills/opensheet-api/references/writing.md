# Writing rows

Writes need `sheets:write` **and** a sheet marked writable. Check `capabilities.writable`
before assuming a `403` is a scope problem.

## Create one row → 201

```bash
curl -X POST "$S/rows" \
  -H "Authorization: Bearer $OPENSHEET_API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"name":"Ada","amount":"12"}'
```

Returns the created row, including its generated `id`, plus an `ETag`. Unknown column names
are rejected.

## Create many → 201

```bash
curl -X POST "$S/rows/batch" ... -d '{"rows":[{"name":"Ada"},{"name":"Bob"}]}'
```

Returns `{"ids":[...]}` in request order. The batch is bounded; oversized bodies fail with
`SHT028`.

## Append raw cells → 200

```bash
curl -X POST "$S" ... -d '{"values":["Ada","12"]}'
```

Positional, by column order, bypassing header mapping. Returns `{"appended":1}`. Prefer
`POST /rows` — append does not generate an `id` and will not line up if someone reorders
columns in Google.

## Update → 200

```bash
curl -X PATCH "$S/rows/<id>" ... -H 'If-Match: "<etag>"' -d '{"amount":"20"}'
```

- `PATCH` updates only the keys you send.
- `PUT` replaces every column; omitted columns are blanked.

## Delete → 204

```bash
curl -X DELETE "$S/rows/<id>" -H "Authorization: Bearer $OPENSHEET_API_KEY" -H 'If-Match: "<etag>"'
```

Soft when the tab has a soft-delete column (`capabilities.softDelete`), otherwise the row is
removed outright.

## Idempotency

`Idempotency-Key` is keyed to the request body. Replaying the same key with the same body
returns the first result instead of writing again; replaying it with a *different* body is
rejected. Use a fresh UUID per logical write, and reuse it only when retrying that write.

Without a key, a retry after a timeout appends a duplicate — the spreadsheet has no unique
constraint to stop it.

## Concurrency

`If-Match` carries the `ETag` from the read that produced the row. A mismatch returns `412`,
meaning someone changed the row first: re-read, reapply, retry. Omitting `If-Match` skips the
check and silently overwrites.

## Why a write can fail on the table's shape

Row-addressed routes need an `id` column, and writes need the tab to satisfy the table
contract. `capabilities.satisfiesContract` and `contractReason` say what is wrong. The web UI
offers to add a missing `id` column and backfill it; that writes to the user's spreadsheet,
so it is a deliberate action rather than something a write triggers implicitly.
