# opensheet data plane

The template page is [`surfaces`](README.md). opensheet's product is its data plane (S3,
`internal/dataplane/`, `/api/v1/`): a published sheet's rows as JSON. The route list is in
[`multitenancy/opensheet.md`](../multitenancy/opensheet.md#routes); the integrator-facing guide
is the `opensheet-api` skill in [`skills/opensheet-api/`](../../skills/opensheet-api/SKILL.md).

The registry (credentials, spreadsheets, sheets, API keys) is the control plane: the console and
Connect-RPC.

## Credentials

The data plane accepts two credential classes, routed by shape (`dataplane.NewCredentialAuthorizer`):

| Credential                      | Admitted when                                    | Reach                                  |
| ------------------------------- | ------------------------------------------------ | -------------------------------------- |
| API key (`api.keyPrefix`)       | scope held, path in the key's project            | the project, narrowed by `ResourceIDs` |
| Othra token (`tokens.audience`) | person is a member of the path's org, scope held | every sheet of the project             |

- An access token is verified before the path resolves. An invalid one (bad signature, unknown
  key, wrong audience, an ID token) is `GEN002` with `401`, and an expired one is `TKN001` with
  `401`. Both carry `WWW-Authenticate: Bearer error="invalid_token"` naming the API's RFC 9728
  document. The answer does not depend on the path, so it reveals no slug.
- Every other refusal is the masked `SHT001` 404, for either class: a bad key, a person who is
  unknown or not a member of the path's org, a missing scope.
- A credential check that cannot run (issuer, key store or membership store down) is `TKN002`
  with `503`, so a caller retries instead of reading the sheet as gone. An issuer outage answers
  before the path resolves.
- With `tokens.issuer` empty, every token gets the masked 404 and the data plane is keys only.

## Writes

- A write needs `sheets:write` plus `sheets.writable` on the sheet. The tabs routes need
  `spreadsheets:read` / `spreadsheets:write`, and `spreadsheets.writable` to create.
- `POST …/sheets/{slug}`, `POST …/rows` and `POST …/rows/batch` honour `Idempotency-Key`; a retry
  under one key replays the first attempt's response rather than writing again.
- `PATCH`, `PUT` and `DELETE` on a row honour `If-Match` against the row's `ETag`. A stale tag is
  `412` and writes nothing; an absent header is last-write-wins. `PUT` and `DELETE` are idempotent
  by construction, and a delete of an already-deleted row is `204`.
- `DELETE` is a soft delete: it stamps the tab's `deleted_at` column.

## Filtered reads

Any of `?where=`, `?sort=`, `?limit=` or `?cursor=` switches the whole-tab `GET` from serving the
stored snapshot verbatim to querying the row projection.

```
?where=col:op:value     # repeated; every clause ANDs
?where=col:num.gte:100  # a num or date hint compares typed instead of as text
?sort=col:num.desc      # col:[hint.]asc|desc
?limit=100              # 1 to sheets.maxQueryRows
?cursor=<opaque>        # the keyset position carried by a Link header
```

- Operators: `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `contains`, `starts`, `in`, `empty`, `present`.
  Without a hint every comparison is text. `contains` and `starts` are case-insensitive with `%`
  and `_` literal; `in` takes a comma-separated list; `empty` and `present` take no value
  (`col:empty`). A clause splits on its first two colons only, so a value may contain colons.
- An unknown column, operator or query parameter, a `limit` outside the cap and a malformed
  `cursor` are each `400`, so a typo cannot silently serve the whole tab. Zero matches is `200 []`.
- A page with a successor carries `Link: <?…&cursor=…>; rel="next"`; its absence is the only
  end-of-walk signal. `limit` defaults to `sheets.maxQueryRows`, so a filtered read is never
  unbounded. A sorted walk stops at `sheets.maxSortPages` pages.
- The cursor carries the content digest it was issued against: a sheet edited mid-walk refuses it
  with `409`, and a TTL refresh that changed nothing leaves the walk going.
- A filtered read has its own `ETag`, hashed from the sheet's generation and the canonical query,
  so clause order does not move it. `If-None-Match` returns `304` with no `Link`.
- `sheets.maxPayloadBytes` bounds the stored snapshot, not a filtered body, so a tab over the cap
  is queryable while its unfiltered read is `413`.

## Capabilities

`…/capabilities` is authorized like the rows `GET` and answered from the projection and the
`sheets` row alone, with no Google call, so it is cheap to poll. It reports the columns, the `id`
column, the live row count and whether the tab still satisfies the table contract. Publishing a tab
reads it once and fails closed without an `id` column; `capabilities` is a reserved slug.
