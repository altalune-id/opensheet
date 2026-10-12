# Adapters: transactions, versions, constraint translation

Adding a `Store` verb end to end is
[`howto/store-method.md`](../../../../docs/howto/store-method.md). The two-guard model is
[`multitenancy`](../../../../docs/multitenancy/README.md#two-guards-not-one). `factory.go` and the
adapter's place in the module are
[`modules`](../../../../docs/modules/README.md#2-inside-a-module).

These are the mechanics that recipe assumes.

## Transaction enrollment

`db.Pool{W, R}` wraps writer and reader handles. Two composable helpers, both enrolling via the
same `db.CurrentTx(ctx)` slot: `db.RunInTx(ctx, pool, fn)` (no tenant scope) and
`tenant.RunInTx(ctx, pc, tc, fn)` (sets `app.current_org_id` so RLS sees the org).

**Every store method starts with `txAcquire` and ends with `endTx`.** Copy the pair from
`internal/blog/postgres.go`; do not hand-roll the enrollment.

```go
func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error)
func (s *postgresStore) endTx(tx *sql.Tx, owned bool, err error) error
```

- `db.CurrentTx(ctx)` returns `(*sql.Tx, bool)` — comma-ok, not a nil check.
- `pc.BeginTenanted(ctx, tc)` returns `(*sql.Tx, error)`. It takes no callback.
- The `owned` bool is what tells `endTx` whether to commit or leave an outer transaction alone.
- Nesting `RunInTx` returns `db.ErrNestedUnitOfWork`. A multi-statement write — a row plus its
  join rows — must be one transaction on both drivers; SQLite deadlocks on `SQLITE_BUSY`
  otherwise.

## Tenant predicates

Every read and write carries an explicit `org_id` predicate from `tenant.From(ctx)`
([`howto/store-method.md`](../../../../docs/howto/store-method.md#tenancy)). The part that is
easy to miss: **a method that already takes an `orgID` argument layers both** —
`posts.OrgID.EQ(orgID).AND(posts.OrgID.EQ(tc.OrgID))` — so a caller passing an org that disagrees
with the request's tenant scope matches nothing.

The upsert conflict-clause guard and the build-failing
`TestStoreUpserts_GuardConflictClauseByOrg` are in
[`howto/store-method.md`](../../../../docs/howto/store-method.md#gotchas). One shape it accepts
that is not obvious: `ON_CONFLICT(OrgID, UserID)` — the org in the conflict _target_ — is a
stronger guard than a `WHERE` and satisfies the test.

## Optimistic concurrency

`Save(ctx, p, ifVersion)` / `Delete(ctx, id, ifVersion)`, `0` unconditional, and the SECURITY
bounds check in `pgVersionGuard` / `sqliteVersionGuard`:
[`howto/store-method.md`](../../../../docs/howto/store-method.md#gotchas). The UPDATE branch bumps
the column itself — `s.posts.Version.SET(s.posts.Version.ADD(postgres.Int32(1)))`.

**`RowsAffected() == 0` is ambiguous** — the org guard refused, or the version moved on. Route it
through a `refusalError` helper that re-reads the row's version **inside the same transaction**:
no row → `&NotFoundError{}`; row present and `ifVersion != 0` → `&StaleVersionError{Want, Got}`.
Without that re-read a stale write reports "not found" and the client retries forever.

That a conditional write must be **one** write is
[`howto/service-method.md`](../../../../docs/howto/service-method.md#hard-rules);
`TestSQLite_UpdateWithTagsHonoursThePreconditionWhole` and its Postgres twin pin it here.

## SQL NULL

Never wrap jet's `NULL` singleton — it is a package-level var and wrapping it races under
`make check`. Use the `Null*` helpers in `internal/platform/db/entity/{postgres,sqlite}`:
`NullText`, `NullDate`, `NullTimestampz`, `NullUUID`, `NullJSONB` on Postgres, `NullText` on
SQLite. Each builds a fresh `CAST(NULL AS ...)`. Pick the one matching the column's **declared**
type ([`howto/store-method.md`](../../../../docs/howto/store-method.md#gotchas)).

## Constraint translation

No driver error travels upward
([`howto/store-method.md`](../../../../docs/howto/store-method.md#gotchas)).

**Postgres** — match on `*pgconn.PgError` SQLSTATE:

| Condition                    | Code    |
| ---------------------------- | ------- |
| unique violation             | `23505` |
| foreign key still referenced | `23503` |

**SQLite** — match on the errcode, not the message. Follow `internal/project/sqlite.go`, not
`internal/org/sqlite.go` which string-matches. The import alias matters: `sqlite` is already
go-jet in every store file, so the driver must be aliased.

```go
import (
	"github.com/go-jet/jet/v2/sqlite"
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var sqliteErr *sqlitedrv.Error
if errors.As(err, &sqliteErr) {
	switch sqliteErr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return &AlreadyExistsError{Slug: c.Slug}
	// ON DELETE RESTRICT reports TRIGGER, not FOREIGNKEY — FK actions run as internal triggers;
	// only insert-side violations report FOREIGNKEY.
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY, sqlite3.SQLITE_CONSTRAINT_TRIGGER:
		return &InUseError{ID: id.String()}
	}
}
```

## Timestamps

Every SQLite timestamp write goes through `sqliteent.SQLiteTime(t)`, which zero-pads. Never
`Format(time.RFC3339Nano)` — it trims trailing zeros, so text comparison disagrees with
chronological comparison and ordering silently breaks. This applies to **bind sites too**: a
sweep cutoff compared against padded rows must itself be padded, or stale rows are never swept.

## Ordering

Every `List` orders by a total order
([`howto/store-method.md`](../../../../docs/howto/store-method.md#gotchas)). If a read goes
through a `SECURITY DEFINER` wrapper function, repeat the `ORDER BY` on the **outer** statement
and alias the set-returning function. A `SELECT` without `ORDER BY` has no guaranteed row order
regardless of what the function body does.
