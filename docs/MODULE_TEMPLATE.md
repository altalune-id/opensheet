# altempl module template

Every business-domain package under `internal/<name>/` follows this shape.
It lets any engineer land in any module and know where things live.

For infrastructure primitives (adapters, workers, kits), see
[`PLATFORM_TEMPLATE.md`](PLATFORM_TEMPLATE.md).

Reference implementation: `internal/todo/`. When in doubt, copy from there.

## 1. Required files

Exact names.

| File                           | Purpose                                                         |
| ------------------------------ | --------------------------------------------------------------- |
| `<name>.go`                    | Aggregate root type + `New()` with invariants + value types     |
| `store.go`                     | `Store` interface (the driven port)                             |
| `errors.go`                    | Typed error structs + `Is<TypeName>` helpers + `ToAppError()`   |
| `service.go`                   | `type Service struct` + application methods + `NewService(...)` |
| `factory.go`                   | `NewStore(cfg, db, pc) Store` — driver dispatch                 |
| `postgres.go`                  | `postgresStore` on `pgx` via `tenant.PgConn`                    |
| `sqlite.go`                    | `sqliteStore` on `*sql.DB`                                      |
| `<name>_test.go`               | Aggregate invariant tests                                       |
| `service_test.go`              | Application tests using `internal/testutil/fakes.<Name>`        |
| `sqlite_test.go`               | SQLite `:memory:` unit tests                                    |
| `postgres_integration_test.go` | `//go:build integration`; requires `TEST_PG_DSN`                |

Optional, only when the module has periodic work:

| File                | Purpose                                                                                       |
| ------------------- | --------------------------------------------------------------------------------------------- |
| `scheduler.go`      | Optional. `Scheduler` adapter implementing `scheduler.Provider` — one `Job` per periodic task |
| `scheduler_test.go` | Job metadata + `Run` behavior, using the module's fake `Store`                                |

Stateful workflows may live in their own file (see Section 3).

## 2. Conventions

### Package boundary

- One package per bounded context. Package name IS the domain term
  (`todo`, `org`, …) — never `todoservice`, `todo_module`, `todos_v1`.
- Import allow-list for `<name>.go` / `store.go` / `errors.go`
  (enforced by depguard): stdlib, `github.com/google/uuid`,
  `altalune.id/template/internal/platform/tenant`,
  `altalune.id/template/internal/apperror`,
  `altalune.id/template/gen/go/apperror/v1`. Nothing else — no `net/http`,
  no `database/sql`, no `config`.
- `service.go` may add `log/slog`, `go.opentelemetry.io/otel`, and other
  modules' aggregate types by ID only (never mutate another module's aggregate).
- `postgres.go` / `sqlite.go` may import persistence packages.

### Aggregate

- `type <Name> struct { … }` with exported fields.
- `func New(...) (*<Name>, error)` enforces creation invariants — returns
  typed domain errors, never `fmt.Errorf` alone.
- Mutations live on the aggregate (`t.Toggle()`, `o.AddMember(m)`).
- Timestamps: `time.Time` UTC. IDs: `uuid.UUID`. No strings where a type exists.
- No JSON tags on the aggregate — wire shapes live in `api/` / `web/`.

### Store (driven port)

- `type Store interface { … }` in `store.go`.
- Verbs only: `Save`, `ByID`, `List`, `Delete`. Never `Get*` / `Find*`.
- `context.Context` first. Domain-shaped results. Typed errors for
  expected failures.
- No transactions in the Store signature — atomicity is done via the
  Unit-of-Work helpers below, not by passing `*sql.Tx` around.

### Unit of Work

`db.Pool{W, R}` wraps writer + reader `*sql.DB`. SQLite aliases `R` to `W`.
For Postgres, `ALT_DB_READER_DSN` routes non-tenant reads (`users`,
`onboard`) to a replica; when empty, `R` aliases `W`.

Two helpers compose:

- `db.RunInTx(ctx, pool, fn)` — plain writer transaction, enrolled via
  `db.CurrentTx(ctx)`. Cross-cutting flows without tenant scope.
- `tenant.RunInTx(ctx, pc, tc, fn)` — tenant-scoped transaction
  (`set_config('app.current_org_id', ...)` so RLS sees the current org).
  Same `db.CurrentTx` slot. Tenant-scoped flows.

Store method contract:

```go
func (s *postgresStore) Save(ctx context.Context, t *Todo) error {
    if tx := db.CurrentTx(ctx); tx != nil {
        return s.saveTx(ctx, tx, t)      // enroll in outer UoW
    }
    return s.pc.BeginTenanted(ctx, func(tx *sql.Tx) error {
        return s.saveTx(ctx, tx, t)
    })
}
```

Nested `RunInTx` returns `db.ErrNestedUnitOfWork`. Tenant-scoped reads
currently run on `pool.W` (the `set_config` requires an open tx on the
primary) — replica routing for those is a future concern.

### Errors

Every failure mode is a typed struct in `errors.go`:

```go
type NotFoundError struct{ ID string }
func (e *NotFoundError) Error() string { … }
func (e *NotFoundError) ToAppError() *apperror.AppError { … }
func IsNotFoundError(err error) bool {
    _, ok := errors.AsType[*NotFoundError](err)
    return ok
}
```

- One typed error per failure mode. No `Kind int` enum.
- Helper is `Is<FullTypeName>` — always. Type `NotFoundError` →
  `IsNotFoundError`. Type `InvalidTitleError` → `IsInvalidTitleError`.
  Never `IsErr*`, never shortcuts.
- Wire code calls `apperror.AsAppError` — never switches on error types.
- `Error()` format: `"<module>: <situation>: <cause>"`
  (e.g. `"todo: title: over 200 characters"`).

### Service (driving port)

```go
type Service struct {
    store      Store
    log        *slog.Logger
    unexpected apperror.UnexpectedFunc  // function type, not interface
    // module-specific deps after the canonical three
}

func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, ...) *Service
```

- `apperror.UnexpectedFunc` is a Go function type (like `http.HandlerFunc`).
  In prod: `reporter.Unexpected` method value. In tests: one-line lambda.
- Every method opens a span:
  ```go
  ctx, span := tracer.Start(ctx, "<name>.<Method>")
  defer span.End()
  ```
  Package-level `var tracer = otel.Tracer("altalune.id/template/internal/<name>")`.
- Expected failures return typed domain errors directly.
- Unexpected failures go through
  `s.unexpected(ctx, "<name>.<Method>: <situation>", err, k, v...)`.

### Factory

```go
func NewStore(cfg config.DBConfig, db *sql.DB, pc *tenant.PgConn) Store {
    if cfg.Driver == config.DriverPostgres {
        return newPostgresStore(pc)
    }
    return newSQLiteStore(db)
}
```

Each module owns its factory. No central `newRepos(...)` tuple.

### Adapters

- `type postgresStore struct { pc *tenant.PgConn }` — Postgres uses the
  tenant-scoped connection.
- `type sqliteStore struct { db *sql.DB }` — SQLite adds `WHERE org_id = ?`
  from `tenant.From(ctx)`.
- Translate driver errors to typed domain errors — never leak `*pgx.PgError`
  or `sqlite3.Error`:
  ```go
  if errors.Is(err, sql.ErrNoRows) { return nil, &NotFoundError{ID: id.String()} }
  if isUniqueViolation(err) { return nil, &AlreadyExistsError{Field: "slug", Value: slug} }
  ```
- Use `pgx.CollectRows` + `pgx.RowToStructByName` — no manual scanning
  unless irregular.

### Tests

- `<name>_test.go` — aggregate invariants, table-driven, pure Go.
- `service_test.go` — uses `fakes.<Name>`. One `t.Run(name, ...)` per branch.
- `sqlite_test.go` — `:memory:` DB + SQLite migrations.
- `postgres_integration_test.go` — `//go:build integration`, uses
  `pgtest.New(t)` or `TEST_PG_DSN`, cleans up per-test.
- No `testify`, no `sqlmock`, no `gomock`. Hand-written fakes.
- Coverage floor: aggregate ≥ 90%, service ≥ 85%.

## 3. Stateful workflows

A `Service` method that needs 3+ dependencies beyond `store/log/unexpected`
becomes its own struct in its own file. Examples:

- `internal/user/onboard.go` — `OnboardWorkflow` (users, orgs, projects,
  invites Store + policy).
- `internal/invite/send.go` — `SendWorkflow` (invites Store + mailer + baseURL).
- `internal/invite/accept.go` — cross-aggregate (invite, user, org).
- `internal/auth/local.go` — `LocalLogin` (users Store + genesis).
- `internal/auth/oidc.go` — `OIDCLogin` (users Store + ensureFromOIDC + onboarder).

Rule: function unless it has state or 3+ deps. Then struct with a
constructor + one primary method. The `Service` composes it.

## 3a. Periodic work

A module with periodic work adds `scheduler.go` holding a `Scheduler`
struct and `SchedulerJobs() []scheduler.Job` (`scheduler.Provider`).
Reference impl: `internal/todo/scheduler.go`.

- A `Job.Run` body calls the module's own `Service` — never a `Store`
  directly, and never another module's internals.
- Cadence is a package constant in `scheduler.go`, not config. Only the
  timezone is operator-tunable, via `scheduler.jobs.<name>.timezone`.
- A `ScopeTenant` job's `Run` receives an **already tenant-bound ctx**:
  the Runner fans out over tenants and sets the tenant scope before
  each call, so the job needs no RLS or `org_id` handling of its own.
- A `ScopeSystem` job runs once per tick with no tenant scope.
- Register the adapter in `internal/boot/schedulers.go` and add the
  module to `schedulerDomains` there — the wiring assertion fails the
  boot if a slot is missing.

## 4. When to split into subdomains

**Add files first.** More files in one package is not a problem.

Split into `internal/<name>/<sub>/` ONLY when:

1. The subdomain has its own aggregate root with its own invariants.
2. The subdomain has its own `Store` (own tables).
3. Cross-subdomain refs work by ID only.

Cross-references never use pointers to another aggregate — always the
UUID (`comment.Comment.TodoID uuid.UUID`, never `*todo.Todo`).

For sibling-protected helpers, use Go's built-in `internal/`:

```
internal/todo/tag/
├── tag.go, service.go
└── internal/
    ├── slugger/           # only tag/ + tag/**/ can import
    └── colorpalette/
```

## 5. Anti-patterns

- `internal/todo/domain/`, `application/`, `infrastructure/` — never split by layer.
- `type TodoCreateUseCase struct { … Execute(ctx, ...) }` — use `Service.Create(...)`.
- Repository interfaces in a separate `repository/` package.
- `Repository` as the interface name — use `Store`.
- `NewRepo(...)` — use `NewStore(...)`.
- `postgres_repo.go`, `sqlite_repo.go` — use `postgres.go`, `sqlite.go`.
- Sentinel error variables (`var ErrNotFound = errors.New(...)`).
- `Kind int` inside a generic domain error.
- `panic(...)` on missing tenant — return an error.
- `MustFrom(ctx)` returning a value on panic — return `(Context, error)`.
- Cross-module imports for persistence (`import "…/user"` inside
  `invite/postgres.go`) — cross-module refs are by ID via the service.
- `database/sql` in application code — go through `Store`.
- `log.Println(...)` — use `slog.InfoContext(ctx, ...)`.
- `fmt.Print*` / `os.Stdout` in domain code — output belongs to the interface layer.

## 6. Adding a new module — checklist

1. `cp -r internal/todo/ internal/<name>/`.
2. Rename package (`todo` → `<name>`), type (`Todo` → `<Name>`),
   identifier prefixes. `sed -i` works; run `goimports` after.
3. Add fake at `internal/testutil/fakes/<name>.go` implementing `<name>.Store`.
4. Add one line to `internal/boot/server.go`:
   ```go
   <names> := <name>.NewService(<name>.NewStore(cfg.DB, db, pgConn), log, reporter.Unexpected)
   ```
   Plus one field on `type Server struct { … }`.
5. Add migrations under `schema/migrations/{postgres,sqlite}/` named
   `NNN_create_<names>.{up,down}.sql`. If tenant-scoped, add the RLS
   policy in the Postgres migration and run `make tenant-tables`.
6. Exposing on the API? Add `api/<name>/v1/*.proto` and `make generate`.

## 7. Review checklist

- File set matches Section 1 (no `postgres_repo.go`, no `domain/` subpackage).
- `Store` in `store.go` alongside the aggregate.
- Every method takes `context.Context` first.
- Every DB-touching method has `tracer.Start(ctx, "<name>.<Method>")`.
- Expected failures use typed errors + `Is<TypeName>` helpers.
- Unexpected failures go through `s.unexpected(ctx, ...)`.
- `Error()` follows `<module>: <situation>: <cause>`.
- Every typed error has `ToAppError()` returning a stable `apperror.Code*`.
- No `panic` in the code path.
- Cross-module refs by aggregate ID only.
- Aggregate + service tests use fakes (no `:memory:` DB in `service_test.go`).
- SQLite tests exist and pass.
- Postgres integration tests exist under `//go:build integration`.
- Coverage on aggregate ≥ 90%, service ≥ 85%.
- `depguard` clean.
- Adapter file names are `postgres.go` / `sqlite.go`.
- `errors.go` has 1-line godoc on exported types only.
