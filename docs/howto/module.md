# Add a domain module

Use this when a genuinely new bounded context is needed. To change one that already exists, see
[`service-method.md`](service-method.md), [`store-method.md`](store-method.md) and
[`subdomain.md`](subdomain.md).

The file set, the layering rules and the anti-patterns are the contract:
[`modules`](../modules/README.md). This is the order to do them in.

## Before you start

- A cross-cutting primitive is not a module — [`platform`](../platform/README.md).
- A second aggregate inside an existing context is not a module either —
  [`subdomain.md`](subdomain.md).

## Steps

1. `.agents/skills/altalune-go-convention/scripts/scaffold.sh <name>` copies `internal/todo/` and
   renames the package, type and identifiers. A starting point, not a finished module.
2. Migrations: `schema/migrations/postgres/NNN_<name>.sql` **and** the SQLite mirror. Bump both
   `VERSION` files — the dialects are pinned independently. RLS block per
   [`multitenancy`](../multitenancy/README.md#adding-a-tenant-scoped-table).
3. `make tenant-tables`, then confirm the table appears in `schema/tenant_tables_gen.go`.
4. Jet bindings, hand-written, one per dialect:
   `internal/platform/db/entity/{postgres,sqlite}/<table>.go`. Copy `entity/postgres/orgs.go`.
5. Error codes: constants in `internal/apperror/codes.go` **and** rows in
   [`error codes`](../errors/README.md). `TestCodes_EveryRefIsDocumented` checks both directions.
6. Domain files — `<name>.go`, `store.go`, `errors.go`, `service.go`, `factory.go`. Shape and
   import limits: [`modules`](../modules/README.md) Section 1 and Section 2.
7. Adapters and the fake: [`store-method.md`](store-method.md) is the per-verb loop.
8. Boot, two files: construction plus a field on `Services` in `internal/boot/services.go`, then a
   field on `Server` and its assignment in `internal/boot/server.go`.
9. Pick the surfaces. Every route belongs to exactly one — [`surfaces`](../surfaces/README.md).
10. Extend the `domain-purity` file globs in `.golangci.yaml` if the domain file names differ from
    `<name>.go` / `store.go` / `errors.go`.
11. `make check`, then `make test-integration`, then
    `.agents/skills/altalune-go-convention/scripts/verify.sh`.

## Tenancy

Tenant-scoped is the default: the table carries `org_id` and `project_id`, the migration carries the
RLS block, the store opens `pc.BeginTenanted(ctx, tc)` and every query names `org_id`, and the
service reads `tenant.From(ctx)`.

A module whose table has no `org_id` — global like `user`, or pre-tenant like `platform/session` —
changes four things:

| Tenant-scoped                       | Not tenant-scoped                                          |
| ----------------------------------- | ---------------------------------------------------------- |
| RLS block in the PG migration       | none; the table stays out of `tenant_tables_gen.go`        |
| `pc.BeginTenanted` / `txAcquire`    | `db.CurrentTx(ctx)` else `pool.W` — see `user/postgres.go` |
| `WHERE org_id = tc.OrgID`           | nothing; the row id is the whole authority                 |
| guarded `DO_UPDATE` conflict clause | a justified row in `upsertGuardExemptions`                 |

What stops protecting you: both guards at once. RLS has no policy to enforce and the explicit
predicate is gone, so nothing confines a row to a tenant. Only aggregates that genuinely span orgs
qualify.

## Gotchas

- The `{{if .RLSEnforce}}` wrapper is mandatory; `make check` passes without it and a fork with RLS
  off fails at migrate time.
- `make tenant-tables` keys off the literal `ENABLE ROW LEVEL SECURITY` line carrying
  `{{.TablePrefix}}<name>`, not off the `org_id` column. Drop the template literal from the table
  name and the table silently misses both the generated list and the boot audit.
- Never edit an applied migration. Goose tracks a version and does not checksum — the edit is a
  silent no-op.
- A new column needs adding to both bindings **and** to `AllColumns`, or `INSERT(AllColumns)` and
  the row struct disagree without erroring.
