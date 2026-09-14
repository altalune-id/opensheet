# Failures

Every failure is the same envelope:

```json
{"error":{"code":"SHT013","message":"row not found"}}
```

Log the `code`. It is stable and greppable; the message is for humans and may be localised.
The full table lives in `docs/ERROR_CODES.md`.

## Status meanings

| Status | Meaning | First thing to check |
|---|---|---|
| `304` | Not modified | Expected when sending `If-None-Match`. |
| `400` | Malformed request | Clause syntax, unknown query param, bad JSON. |
| `403` | The *sheet* forbids it | `SHT009` not writable, `SHT006` public reads disabled. Not a key problem. |
| `404` | Unknown slug **or** the key may not touch it | See masking below — this is the common one. |
| `409` | Conflict | Duplicate id (`SHT012`), or `Idempotency-Key` replayed with a different body. |
| `412` | Precondition failed | `If-Match` is stale (`SHT030`) — re-read and retry. |
| `413` | Body too large | `SHT007`/`SHT028` — split the batch. |
| `429` | Google's rate limit | Honour `Retry-After`. Cache harder; purge less. |
| `502` | Google unavailable | Retry with backoff. |

**There is no `401` on these routes.** A missing, malformed or wrong bearer token returns
`404`, not `401` — do not wait for a `401` to detect a bad key.

## The 404 mask

`handler.go` states it directly: *"every refusal becomes the shared NotFoundError, so a key
for another project, a key missing the scope, a key not granted this sheet and a slug that
does not exist are one indistinguishable answer."*

So `404` covers all five of:

- the slug really does not exist
- no `Authorization` header at all
- the token is malformed or revoked
- the key belongs to another project
- the key lacks the scope, or was restricted to other sheets

Work through them in that order. The server logs the real reason under the request id; the
response deliberately withholds it so slugs cannot be enumerated by probing.

## Code prefixes

| Prefix | Area |
|---|---|
| `SHT` | published sheet and its rows |
| `SPR` | registered spreadsheet |
| `KEY` | API key auth and scopes |
| `CRD` | Google credential |
| `GSH` | Google's answer, passed through |

`GSH001` (not found) on a document you can open yourself usually means the Google credential
never received a `drive.file` grant for it — re-add the spreadsheet through the Picker rather
than by pasting its URL.
