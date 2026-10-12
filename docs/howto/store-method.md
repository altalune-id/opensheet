# Add a Store method

Use this when a `Service` needs a verb its `Store` does not have. One interface change is four
edits — Postgres, SQLite, the fake, and tests on both real drivers.

`Store` is the driven port the module owns: [`modules`](../modules/README.md)
Section 2. Verbs only — `Save`, `ByID`, `List`, `Delete`; never `Get*` or `Find*`;
`context.Context` first; no `*sql.Tx` in a signature.

## Steps

1. Add the verb to `<module>/store.go` with a 1-line godoc naming its failure modes.
2. Postgres in `postgres.go` (or `pgreader.go` / `pgwriter.go` once it grows): acquire through
   `txAcquire` / `endTx`, and name `org_id` explicitly —
   `s.posts.OrgID.EQ(postgres.UUID(tc.OrgID))`.
3. SQLite in `sqlite.go`: the same predicate, `sqlite.String(tc.OrgID.String())`. SQLite has no
   RLS, so there the predicate is the only guard.
4. The fake in `internal/testutil/fakes/<name>.go`. `var _ <module>.Store = (*<Name>)(nil)` makes
   the missing method a compile error.
5. Tests, paired: `sqlite_test.go` (`:memory:` or a temp-file DSN) and
   `postgres_integration_test.go` (`//go:build integration`, `pgtest.New(t)`). Name them
   `TestSQLite_<Behaviour>` / `TestPostgres_<Behaviour>` so the pair is greppable.
6. `make check`, then `make test-integration`.

The one shared contract harness in the tree is `runStoreContract` in
`internal/platform/session/store_contract_test.go`, called from both that package's `sqlite_test.go`
and `postgres_integration_test.go`. Domain modules pair test names instead; a new `Store` whose
semantics are identical across drivers is a good candidate for the `runStoreContract` shape.

## Building the query

**Default to the go-jet sqlbuilder; drop to raw SQL only where jet has no builder.** Copy
`internal/blog/postgres.go` — typed columns, `postgres.SELECT(...).FROM(...).WHERE(...)`.

| Need               | Use                                                                                                                                                   |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| table/column types | `internal/platform/db/entity/{postgres,sqlite}` — hand-written, one file per dialect, no generator and no make target ([module.md](module.md) step 4) |
| execute            | `stmt.QueryContext(ctx, tx, &rows)` / `stmt.ExecContext(ctx, tx)` — jet's own executors, not raw SQL                                                  |
| no row             | `qrm.ErrNoRows` / `sql.ErrNoRows` → the module's `*NotFoundError`                                                                                     |
| a `NULL` literal   | the `Null*` helpers, never jet's `NULL` singleton — see Gotchas below                                                                                 |

**The one raw construct** is a `SECURITY DEFINER` wrapper — a set-returning function in `FROM`
position, which go-jet cannot build. Four precedents, all Postgres: `internal/org/postgres.go:18`,
`internal/invite/postgres.go:40`, `internal/apikey/postgres.go:41` and
`internal/platform/tenant/orgreader.go:27`; `sqlite.RawStatement` has no call site.

- Each carries a `NOTE:` naming what jet could not express. No NOTE, no raw statement.
- SECURITY: interpolate only config-supplied identifiers (`schema`, `tablePrefix`); bind every value
  as a named argument — `postgres.RawArgs{"#hash": hash[:]}` (`internal/apikey/postgres.go:220`).
  `TestPostgresStore_WrapperStatementsBindValuesNotInterpolate` pins it.

## The fake is not free to differ

**The fake mirrors the real stores' write contract.** Both real stores insert with the caller's
version and, on conflict, `SET version = version + 1`; a nonzero `ifVersion` that misses returns
`*StaleVersionError`. The fake does the same, and
`TestBlogSaveMatchesTheRealStoresVersionContract` / `TestBlogSaveRejectsAStaleVersion` in
`internal/testutil/fakes/blog_test.go` pin it. A fake that skipped the bump, or dropped a field on
the round trip, makes every concurrency test pass while the real store is broken.

**The mirror trap: a fake must not enforce what a test is proving.** The real stores filter by org,
_not_ by project — the project check belongs to the `Service` (`category.Service.ByID`). A fake that
filtered by project would make every project-scoping test pass regardless of the production guard.
Copy the real store's predicate, not a stricter one.

## Gotchas

- **Guard the upsert's conflict clause.**
  `DO_UPDATE(postgres.SET(...).WHERE(t.OrgID.EQ(postgres.UUID(tc.OrgID))))`, plus a
  `RowsAffected() == 0` branch returning `*NotFoundError`. `TestStoreUpserts_GuardConflictClauseByOrg`
  in `schema/upsert_tenant_guard_test.go` fails the build on an unguarded `DO_UPDATE` — fix the
  store, never widen `upsertGuardExemptions`.
- **Version guards are `pgVersionGuard` / `sqliteVersionGuard`.** `0` returns `Bool(true)`.
  SECURITY: `ifVersion < 0 || > math.MaxInt32` returns `Bool(false)`, because narrowing to `int32`
  wraps and could match a live version, turning a conditional write unconditional.
- **Never wrap jet's `NULL` singleton.** Use the `Null*` helpers in
  `internal/platform/db/entity/{postgres,sqlite}`, matched to the column's declared type — Postgres
  has no assignment cast, so a mistyped null fails at analyze time.
- **No driver error travels upward.** `qrm.ErrNoRows` / `sql.ErrNoRows` → `*NotFoundError`, unique
  violation → `*AlreadyExistsError`. See `mapPgConstraint` in `internal/blog/postgres.go`.
- **Every `List` orders by a total order** — `created_at DESC, id DESC`. `created_at` alone is not
  one and the page boundary wobbles.
- **A guard's test must run where the guard is the only protection.** On an RLS-enforcing fixture a
  hijack test passes with or without the code under test. `TestPostgres_Post_OtherOrgIsInvisible_WithoutRLS`
  is the shape; prove each by reverting the guard and watching it fail.

## Tenancy

Default: the method opens a tenant-scoped transaction (`pc.BeginTenanted(ctx, tc)` via `txAcquire`),
carries `WHERE org_id = tc.OrgID`, and joins an outer transaction when `db.CurrentTx(ctx)` holds
one. Both guards, explicitly: [`multitenancy`](../multitenancy/README.md).

On a table with no `org_id` the tenanted begin disappears; the store takes `db.CurrentTx(ctx)` if
present and `pool.W` / `pool.R` otherwise (`internal/user/postgres.go`), there is no `org_id`
predicate, and an upsert needs a justified row in `upsertGuardExemptions`. RLS has no policy either,
so the row id is the entire authority for the write.
