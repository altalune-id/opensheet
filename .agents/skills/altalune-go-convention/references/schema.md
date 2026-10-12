# Schema: migrations, RLS, jet bindings

The procedure — migration files, `VERSION` bumps, `make tenant-tables`, never editing an applied
migration — is [`howto/module.md`](../../../../docs/howto/module.md#steps) and its gotcha list.
The model RLS enforces is
[`multitenancy`](../../../../docs/multitenancy/README.md#adding-a-tenant-scoped-table). This file is
what those two assume you already know.

## The policy shape

Copy the shape of `002_rls.sql`. `ENABLE`, `FORCE` and a policy must all be present or the boot
audit (`schema/rls_guard.go`) rejects the table.

```sql
{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}<table> ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}<table> FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}<table>_tenant
  ON {{.Schema}}.{{.TablePrefix}}<table>
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}
```

**Do not add an explicit `WITH CHECK (true)`.** A bare `CREATE POLICY ... USING (...)` yields
`cmd = ALL`, which satisfies both the read and the write half of the audit. Adding
`WITH CHECK (true)` is a cross-tenant write hole, and the audit rejects it.

The `{{if .RLSEnforce}}` wrapper and the `{{.TablePrefix}}` literal in the `ENABLE` line are both
mandatory, and `make check` passes without either — the failure modes are in
[`howto/module.md`](../../../../docs/howto/module.md#gotchas).

## A table with no `org_id`

Global (`users`) or pre-tenant (`sessions`) tables get no policy and must stay out of the tenant
list. Add a guard test pinning that, and register the table in `schema.RequiredTableSuffixes`
(`schema/table_guard.go`) if boot should verify it exists. What stops protecting you when both guards
go at once: [`howto/module.md`](../../../../docs/howto/module.md#tenancy).

## SQLite counterpart

Same tables, `TEXT` for ids and timestamps, no `{{.Schema}}` prefix, no RLS block. Keep the
`{{.TablePrefix}}` literal on table _and_ index names. SQLite has no RLS, which is why the
adapter's explicit `org_id` predicates are load-bearing there.

## Jet bindings

Adapters use hand-written go-jet bindings, not raw SQL. One file per dialect per table:

```
internal/platform/db/entity/postgres/<table>.go   New<Table>(schema, tablePrefix string)
internal/platform/db/entity/sqlite/<table>.go     New<Table>(tablePrefix string)
```

Copy `entity/postgres/orgs.go` for the shape: typed columns, an `AllColumns` list, a constructor.

- **Do not add a `MutableColumns` field** — it was removed repo-wide because nothing read it.
- **SQLite timestamp columns are `ColumnString`**, not a time type.
- A migration that only adds a column (`009_blog_version.sql`) still needs that column in both
  bindings _and_ in `AllColumns`
  ([`howto/module.md`](../../../../docs/howto/module.md#gotchas)).
- Nullable columns go through the `Null*` helpers in the same packages, never a wrapped jet
  `NULL` — see [`persistence.md`](persistence.md#sql-null).
