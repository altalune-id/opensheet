# Reading rows

## Response shape

A JSON array of objects keyed by the tab's header row. Every value is a string — opensheet
does not infer types.

```json
[{"id":"aB3","name":"Ada","amount":"12"}]
```

Blank headers become `col_1`, `col_2`; duplicates become `name_2`. The UI shows these as
warnings when you publish.

## Filtering

Repeat `?where=` to AND clauses together. Form: `column:operator[:value]`.

| Operator | Meaning |
|---|---|
| `eq` `ne` | equal / not equal |
| `gt` `gte` `lt` `lte` | ordered comparison |
| `contains` `starts` | substring / prefix |
| `in` | value in a comma-separated list |
| `empty` `present` | cell is blank / not blank (no value part) |

```
?where=status:eq:paid&where=amount:gte:100&where=note:present
```

Cells are text, so `amount:gt:9` is a *string* comparison by default. Add a hint on the
operator to compare numerically or by date:

```
?where=amount:num.gte:100        # numeric
?where=due:date.lt:2026-01-01    # date
```

## Sorting and paging

```
?sort=amount:num.desc     # column:[hint.]asc|desc
?limit=50&cursor=<opaque>
```

When more rows remain the response carries a `Link: <...>; rel="next"` header. Follow it
rather than building the cursor yourself.

An unknown query parameter is rejected rather than ignored, so a typo fails loudly instead
of silently returning everything.

## Caching

Reads are served from a cached snapshot with a TTL set per sheet.

- `ETag` — pass back as `If-None-Match` to get `304` and skip the transfer.
- `X-Opensheet-Stale: 1` — the snapshot is past its TTL and a refresh is in flight. The body
  is still usable; retry shortly for fresh data.
- `DELETE /sheets/<slug>/cache` drops the snapshot so the next read refetches from Google.
  It needs `cache:purge`, not `sheets:write`.

Purge sparingly. Every purge forces a Google round-trip, and Google's per-minute read quota
is the binding limit on a busy sheet.

## Reading one row

```bash
curl "$S/rows/<id>" -H "Authorization: Bearer $OPENSHEET_API_KEY"
```

`<id>` is the value in the tab's `id` column, not a row number. Returns `404` when the tab
has no `id` column — check `capabilities.idColumn` first.
