# altempl platform template

Every infrastructure package under `internal/platform/<name>/` follows
this shape. Platform packages are the cross-cutting primitives that
domain modules depend on: config loaders, DB openers, mailers,
notifiers, session stores, tokens, telemetry, tenant scoping, worker
supervisors, request-ID propagation.

The template makes adding a new capability (a Valkey cache, a scheduler,
a queue, a rate limiter) a mechanical slot-in.

For domain packages, see [`MODULE_TEMPLATE.md`](MODULE_TEMPLATE.md).

## 1. What counts as a platform package

**Yes:**

- Adapters over external systems — `authl`, `mailer`, `db`, `tokens`,
  future `cache`, `storage`, `search`.
- Cross-cutting primitives — `logger`, `telemetry`, `reqid`, `tenant`,
  `session`, `capabilities`, `nanoid`, `worker`, `notify`.
- Long-running loops — `scheduler`, `queue` consumer, future `outbox`,
  websocket gateway. All register as `Worker` on `Supervisor`.
  Reference impls: `worker/` (the Supervisor itself), `scheduler/`
  (a `Worker` that owns many periodic `Job`s), and
  `internal/platform/db.HealthMonitor` (a `Worker` owning one loop).

These are **categories, not directories.** Some platform packages live
under `internal/platform/<name>/`; others are exported roots —
`worker/`, `scheduler/`, `logger/`, `telemetry/`, `mailer/`, `nanoid/`,
`reqid/`, `authl/`. A primitive downstream forks may import belongs at
the root; one that only this app's internals need belongs under
`internal/platform/`.

**No:**

- Domain business logic — goes under `internal/<name>/` per MODULE_TEMPLATE.
- API/web/CLI surface — under `internal/{api,web,cli}/`.
- One-off utilities used by a single caller.

## 2. Required files

| File                    | Required?                  | Purpose                                        |
| ----------------------- | -------------------------- | ---------------------------------------------- |
| `<name>.go` OR `doc.go` | Yes                        | Package godoc + primary type / interface       |
| `<name>_test.go`        | Yes                        | Coverage on every exported symbol              |
| `config.go`             | If cfg-driven              | Config struct + defaults (Viper-compatible)    |
| `errors.go`             | If ≥ 1 typed error         | Typed errors + `Is<TypeName>` + `ToAppError`   |
| `options.go`            | If uses functional options | `type Option func(*T)` + `With*` builders      |
| `<subject>.go`          | Per adapter                | `type postgresStore`, `type valkeyCache`, etc. |
| `<subject>_test.go`     | Yes                        | Coverage per adapter                           |

## 3. Conventions

### Constructor

- Exactly one: `func New(cfg <Type>, deps..., opts ...Option) (T, error)`.
- Returns the concrete `*Type` OR the interface it implements.
- No package-level singletons. No `Init()` / `MustInit()`.
- If the constructor performs I/O, first param is `ctx context.Context`.
- If it holds resources, expose `Close() error`. `platform.Kernel.Close()`
  calls it during shutdown.

### Options (when needed)

```go
type Option func(*T)
func WithTimeout(d time.Duration) Option { return func(t *T) { t.timeout = d } }

func New(cfg Config, opts ...Option) (*T, error) {
    t := &T{cfg: cfg, timeout: 30 * time.Second}
    for _, opt := range opts { opt(t) }
    return t, nil
}
```

Use options for testing seams (clock, RNG, in-process fakes) and rare
knobs. Required inputs are positional constructor args.

### Config

- Every cfg-driven package declares its own `Config` in `config.go`
  with `yaml:"..."` tags and defaults.
- The application `config.Config` embeds each platform config:
  ```go
  type Config struct {
      Log    logger.Config `yaml:"log"`
      Tokens tokens.Config `yaml:"tokens"`
      Cache  cache.Config  `yaml:"cache"`
  }
  ```
- An **exported root** primitive carries no `mapstructure` tags and no
  deployment policy — it takes a plain `Options` struct at construction.
  Its config lives in `internal/platform/config` instead, so the
  importable package stays free of Viper. `scheduler/` is the reference:
  ```go
  // internal/platform/config/config.go
  type SchedulerConfig struct {
      Enabled       bool                          `mapstructure:"enabled"       awareness:"bootstrap"`
      Timezone      string                        `mapstructure:"timezone"      awareness:"bootstrap"`
      ShutdownGrace time.Duration                 `mapstructure:"shutdownGrace" awareness:"-"`
      Jobs          map[string]SchedulerJobConfig `mapstructure:"jobs"          awareness:"-"`
  }
  ```
- Env-var mapping is automatic via the `BindEnv` walker in
  `internal/platform/config/`. `ALT_CACHE_URL` binds to `cfg.Cache.URL`.
- Provide `func (c *Config) Validate() error` when constraints are non-trivial.

### Errors

Same discipline as domain modules — typed error structs, `Is<TypeName>`
helpers, `ToAppError()` for wire mapping. Unexpected failures flow
through `apperror.UnexpectedFunc` passed at construction (a Go function
type, like `http.HandlerFunc`). Every long-running or fallible platform
package accepts one. Never `panic` above `TestMain`.

### Logging + telemetry

- Every platform package accepts a `*slog.Logger`.
- Package-level `var tracer = otel.Tracer("altalune.id/template/internal/platform/<name>")`.
- One span per meaningful operation. Attributes: `component`, `operation`, `id`.
- Metrics: OTel meter, not Prometheus registry directly.

### Worker (long-running loops)

Long-running packages implement `worker.Worker`:

```go
type Worker interface {
    Name() string
    Run(ctx context.Context) error
}
```

Registration:

```go
sup := worker.New(log)
sup.Register(sched)                                    // implements Worker
sup.Register(queueCons)                                // implements Worker
sup.Register(worker.HTTP("http", cfg.HTTP.Addr, webHnd, log))
```

Every worker:

1. Returns `nil` on graceful shutdown (ctx canceled).
2. Returns a non-nil error on unrecoverable failure (Supervisor cascades cancel).
3. Never blocks ctx cancellation for more than the graceful window (10s).
4. In a periodic loop, logs per-tick failures instead of returning them — a
   returned error cancels every sibling worker. `db.HealthMonitor.Run`
   swallows probe errors for exactly this reason.

**Worker or scheduler `Job`?** A `Job` when at most one replica should do the
work per tick, or when an operator needs to trigger it by name
(`altempl scheduler run <job>`). A `Worker` when every replica needs its own
copy of the result — per-process state such as a health snapshot — or when
per-tick failures must not reach the scheduler's `ErrorReporter`.

### Import allow-list (depguard-enforced)

Platform packages MAY import: stdlib, third-party libs the primitive
requires, `internal/apperror`, and other platform packages ONLY along
the allow-list (`logger`↔`reqid`+`telemetry`, `tenant`→`apperror`,
`tokens`→`session`, `notify`→`apperror`+`mailer`, `telemetry`→`logger`).

Platform packages MUST NOT import: any `internal/<domain>/` package,
`internal/api`, `internal/web`, `internal/cli`, `internal/boot`.

## 4. Adapter pattern — swappable implementations

When multiple backends satisfy the same port (e.g. cache with memory

- Valkey + Redis adapters):

```
internal/platform/cache/
├── doc.go
├── cache.go        # type Cache interface { Get, Set, Delete, ... }
├── config.go       # Config with Kind: "memory" | "valkey" | "redis"
├── factory.go      # New(cfg, log, unexpected) — dispatches by cfg.Kind
├── memory.go, valkey.go, redis.go     # implementations
├── errors.go
└── cache_test.go   # contract-test table runs against every backend
```

Contract test table:

```go
func TestCache_Contract(t *testing.T) {
    for _, backend := range []struct{ name string; new func() Cache }{
        {"memory", func() Cache { return newMemoryCache() }},
        {"valkey", func() Cache { return newValkeyForTest(t) }},   // t.Skip if no URL
        {"redis",  func() Cache { return newRedisForTest(t)  }},
    } {
        t.Run(backend.name, func(t *testing.T) {
            c := backend.new()
            // Get / Set / Delete / TTL / Expiry / concurrent access
        })
    }
}
```

Every adapter satisfies the same contract. Divergences are either
bugs or documented differences.

## 5. Shape variants

### 5.1 Primitive package (single type)

Example: `reqid/`.

```
reqid/
├── reqid.go
└── reqid_test.go
```

One exported type or one set of exported functions. No config, no I/O.

### 5.2 Adapter package

See Section 4. `factory.go` dispatches by `cfg.Kind`.

### 5.3 Worker package

Example: adding `internal/platform/scheduler/`.

```
internal/platform/scheduler/
├── doc.go
├── scheduler.go        # implements worker.Worker
├── config.go
├── job.go              # type Job interface { Name() string; Execute(ctx) error }
├── registry.go         # func (s *Scheduler) Register(j Job)
├── errors.go
├── options.go
└── scheduler_test.go
```

`Run(ctx)` starts the loop, sets up spans, watches `ctx.Done()`, and
returns `nil` on graceful cancel. Boot wires it:

```go
sched, err := scheduler.New(cfg.Scheduler, log, reporter.Unexpected)
sched.Register(&invite.ExpireJob{Invites: invites})
sup.Register(sched)
```

### 5.4 Queue / broker (producer + consumer)

Producer registers as a Closer (flushes on close). Consumer implements
`worker.Worker`. Same directory layout as adapters. Backend switching
happens in `factory.go`; new topic-handlers land as one line in the
consumer's handler map.

### 5.5 Rate-limit / cache

Same shape as the cache example in Section 4. Same contract-test suite,
same Kind dispatch, same import allow-list.

## 6. Boot integration — the platform.Kernel

Every primitive registers on `platform.Kernel`:

```go
type Kernel struct {
    DB       *sql.DB
    PgConn   *tenant.PgConn
    Log      *slog.Logger
    Reporter *apperror.Reporter
    Sessions session.Store
    Verifier tokens.Verifier
    Mail     mailer.Mailer
    // ... future: Cache, Queue, RateLim, Storage

    closers []io.Closer
}

func (k *Kernel) AddCloser(c io.Closer) { … }
func (k *Kernel) Close() error { … }  // reverse-registration order
```

Reverse-order shutdown is deliberate — DB registers first so it closes
LAST (dependents flush first).

### Shutdown chain

```
SIGTERM / SIGINT
  → main.go — signal.NotifyContext cancels root ctx
  → cli/serve.go RunE returns; deferred s.Close() fires
  → Supervisor.Run(ctx) already returned:
      ├── HTTP worker sees ctx.Done() → srv.Shutdown(10s ctx) → drain
      └── every registered worker exits when ctx cancels
  → Server.Close():
      ├── shutdownOTel(bgCtx) — flush spans + metrics
      └── Kernel.Close() — closers in reverse order
```

Process exits code 0 (`context.Canceled` maps to `ExitOK`), no leaked
goroutines, no dropped requests.

### Adding a primitive — checklist

1. One package under `internal/platform/<name>/` matching this template.
2. One field on `Kernel`.
3. If it holds resources, implement `Close() error` and call
   `kernel.AddCloser(instance)` in `boot.Server` right after construction.
4. If it runs a background loop, implement `worker.Worker` and
   `sup.Register(instance)` — `Run(ctx)` MUST return `nil` on graceful
   exit, never `ctx.Err()`.
5. Domain modules that consume it accept it as a constructor param
   (never reach into `Kernel` from inside a `Service`).

### Registration ordering

- **DB registers first** — closes LAST.
- **Notify sinks register before DB but after their deps** — so incident
  emissions during shutdown still land.
- **Queue producers** register as closers if they buffer (flush on Close).
  Consumers are `Worker`s and close via ctx cancel.

## 7. Anti-patterns

- Package-level singletons (`var Global = ...`).
- `Init()` / `MustInit()` with side effects on import.
- Cross-imports between platform packages outside the allow-list.
- Domain-code imports inside a platform package.
- Anonymous / package-level goroutines started at construction — go
  through `worker.Worker.Run(ctx)`.
- `time.Sleep` in hot paths — use context deadlines + `time.NewTimer`.
- Logging via package-level `log.Println` or `slog.Info` default.
- Panics for expected failure modes.
- Shared mutable state without a `Close()`.

## 8. Adding a new primitive — steps

1. `mkdir internal/platform/<name>/`. Copy from the nearest analogue.
2. Write the port interface in `<name>.go`.
3. Write the config struct in `config.go` with `Validate()` if needed.
4. Write the constructor in `factory.go` (adapters) or `<name>.go`
   (single-type primitives).
5. Write `<name>_test.go`. Adapters use the contract-test pattern.
   Workers cover `Run(ctx)` cancellation semantics.
6. Wire into `internal/boot/server.go`: one field on `Kernel`, one
   construction block, one `sup.Register(...)` if it's a Worker.

Add the config schema to `config.example.yaml` (auto-generated) and
`.env.example` with `ALT_*` env-var mapping.

## 9. Review checklist

- Package name is a noun (`cache`, `queue`) — never `pkg`, `util`, `common`.
- Import allow-list respected.
- Constructor signature: `func New(cfg <Type>, deps..., opts ...Option) (T, error)`.
- Every long-running loop implements `worker.Worker`.
- Every resource-holding type implements `io.Closer`.
- No package-level singletons, no `Init()`.
- Config struct with defaults, `Validate()` where non-trivial.
- `<name>_test.go` covers every exported symbol.
- Adapters: contract-test suite runs against every backend.
- Errors typed with `Is<TypeName>` + `ToAppError()`.
- `slog.Logger` + `apperror.UnexpectedFunc` accepted as constructor deps.
- OTel spans on meaningful operations.
- `Kernel.Close` closes this primitive in reverse-creation order.
- Config documented in `config.example.yaml` with env-var mapping.

## 10. Growth story

The template's promise: every new primitive (Valkey cache, Redis
rate-limiter, NATS queue, PostgreSQL LISTEN/NOTIFY consumer, cron
scheduler, WebSocket gateway, S3 object store, Meilisearch index) lands
the same way — one directory, one interface, one factory, one worker,
one boot line, one field on `Kernel`. The composition root grows by
~5 LoC per new capability. Downstream forks never need to fork the
Kernel structure — they extend it.
