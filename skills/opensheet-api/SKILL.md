---
name: opensheet-api
description: Read and write a published Google Sheet tab over opensheet's HTTP data plane — auth with an API key, filtered and sorted reads, row CRUD, batch writes, caching and concurrency headers, and the JSON error envelope. Use this whenever calling an opensheet endpoint under /api/v1, minting or scoping an API key for one, debugging a 404, 403, 409 or 412 from a sheet URL, or when the task mentions opensheet, a published sheet, a sheet slug, or "read/write my Google Sheet over HTTP".
license: Proprietary
metadata:
  data-plane: internal/data
  error-codes: docs/ERROR_CODES.md
---

# Using the opensheet HTTP API

Every route is scoped to an org, a project and a published sheet slug:

```
https://<host>/api/v1/orgs/<org>/projects/<project>/sheets/<slug>
```

The slug is unique **per project**, not per spreadsheet. Two spreadsheets in one project
cannot both publish `tx`.

## Quick start

```bash
export OPENSHEET_API_KEY='osk_...'          # shown once, at mint time
export S='https://host/api/v1/orgs/acme/projects/main/sheets/tx'

curl "$S" -H "Authorization: Bearer $OPENSHEET_API_KEY"                  # all rows
curl "$S?where=status:eq:paid&sort=amount:num.desc&limit=50" \
  -H "Authorization: Bearer $OPENSHEET_API_KEY"                          # filtered page
curl -X POST "$S/rows" -H "Authorization: Bearer $OPENSHEET_API_KEY" \
  -H 'Content-Type: application/json' -d '{"name":"Ada","amount":"12"}'  # create
```

Rows are JSON objects keyed by the tab's header row; every value is a string.

Check what a sheet allows before writing to it:

```bash
curl "$S/capabilities" -H "Authorization: Bearer $OPENSHEET_API_KEY"
```

It reports `columns`, `idColumn`, `writable`, `softDelete` and `satisfiesContract`
without touching Google.

## Routes

| Method | Path | Scope |
|---|---|---|
| GET | `/sheets/<slug>` | `sheets:read` |
| GET | `/sheets/<slug>/rows/<id>` | `sheets:read` |
| GET | `/sheets/<slug>/capabilities` | `sheets:read` |
| POST | `/sheets/<slug>` | `sheets:write` |
| POST | `/sheets/<slug>/rows` | `sheets:write` |
| POST | `/sheets/<slug>/rows/batch` | `sheets:write` |
| PUT | `/sheets/<slug>/rows/<id>` | `sheets:write` |
| PATCH | `/sheets/<slug>/rows/<id>` | `sheets:write` |
| DELETE | `/sheets/<slug>/rows/<id>` | `sheets:write` |
| DELETE | `/sheets/<slug>/cache` | `cache:purge` |
| GET/POST | `/spreadsheets/<id>/tabs` | `spreadsheets:read` / `:write` |

Details: reads and query syntax → `references/reading.md`. Writes, idempotency and
concurrency → `references/writing.md`. Failures → `references/errors.md`.

## Rules that are expensive to get wrong

**A key is shown once.** `POST /apikeys` returns the plaintext at mint time and stores only
an argon2id hash. There is no endpoint that reads it back — lose it and mint a new one.

**Writes need two separate grants.** The key needs `sheets:write` *and* the sheet itself must
be marked writable in the UI. A key with the scope still gets `403` on a read-only sheet;
`capabilities.writable` tells you which one is missing.

**A key restricted to specific sheets cannot use the `/spreadsheets/.../tabs` routes at all.**
Those authorize project-wide, and a sheet-scoped key is refused there by design.

**Send `Idempotency-Key` on every create.** Retrying without one appends duplicate rows —
the sheet is the store, so there is no unique constraint to catch it.

**Pass `If-Match` on update and delete.** Without it a concurrent writer's change is
overwritten silently. Take the ETag from the read that produced the row.

**Row ids come from an `id` column in the tab**, not from a row number. A tab without one
rejects every by-id route; `capabilities.idColumn` reports this, and the UI offers to add it.

**There is no `401`; a bad key returns `404`.** A missing header, a revoked token, a key from
another project and a slug that does not exist are one indistinguishable answer, so slugs
cannot be enumerated. Never wait for a `401` to detect a bad key — check the key's scopes and
sheet grants before assuming the slug is wrong.

## Verify

`scripts/smoke.sh <base-url> <org> <project> <slug>` reads, filters and inspects one sheet
with `$OPENSHEET_API_KEY`, printing each status. It never writes.
