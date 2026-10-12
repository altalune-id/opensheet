# Add a platform primitive

Use this when several modules or surfaces need one shared capability — a cache, a queue, a rate
limiter, a health probe. A bounded context goes in `internal/<name>/` instead; see
[`modules`](../modules/README.md). The contract, the file list and the
anti-patterns: [`platform`](../platform/README.md).

## Steps

1. **Pick the directory.** `internal/platform/<name>/` unless downstream forks import it, in which
   case it is a root package and every signature change costs every fork.
2. **Write `<name>.go`** — package godoc plus the port type. Copy the nearest analogue:
   `internal/platform/session/` for an adapter over several backends, `internal/platform/outbox/`
   or `db.HealthMonitor` (`internal/platform/db/health.go`) for a loop.
3. **One constructor.** `New` for a single type, `New<Thing>` for a factory. Required inputs are
   positional; `*slog.Logger` and `apperror.UnexpectedFunc` are injected, never reached for. No
   package-level singletons, no `Init()`, no goroutine started at construction.
4. **`config.go`** if it is config-driven — [`config-key.md`](config-key.md).
5. **`errors.go`** for typed errors — [`errors.md`](errors.md).
6. **Tests on every exported symbol.** An adapter runs one contract suite against every backend
   (`store_contract_test.go` in `internal/platform/session/`), never one suite per backend.
7. **Wire `internal/boot/server.go`**: one field on `platform.Kernel`
   (`internal/platform/platform.go`), one construction block, `kernel.AddCloser(x)` if it holds
   resources, `sup.Register(x)` if it runs a loop.
8. **`make config-examples`**, then `make check`.

## If it runs a long-running loop

```go
// Worker is a named long-running task run under a Supervisor.
type Worker interface {
	Name() string
	Run(ctx context.Context) error
}
```

- **`Run` returns `nil` on graceful cancel — never `ctx.Err()`.** `Supervisor.Run`
  (`worker/supervisor.go`) runs every worker under one errgroup and returns the first non-nil
  error, cancelling every sibling. A worker reporting its own clean shutdown as an error takes the
  process down.
- **A periodic loop logs per-tick failures instead of returning them.** `db.HealthMonitor` swallows
  probe errors so a database blip cannot kill the process.
- **Stay inside the 10s graceful window** — `worker.HTTP`'s shutdown budget.
- **Ticker plus `ctx.Done()`**, never `time.Sleep` in a loop.

| Choose          | When                                                                                                                                                  |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| scheduler `Job` | At most one replica does the work per tick, or an operator triggers it by name (`opensheet scheduler run <job>`). Shape: `internal/todo/scheduler.go` |
| `worker.Worker` | Every replica needs its own result, or per-tick failures must not reach the scheduler's error reporter                                                |

## Gotchas

- **Shutdown is reverse registration order.** `Kernel.Close` walks `slices.Backward(closers)`, so
  register a dependency before whatever depends on it. Boot registers the notify sinks before the
  pool, so an incident raised during shutdown still has somewhere to land.
- **The import allow-list is enforced, and `.golangci.yaml` is the source of truth** — read the
  rule, not a doc's summary of it. `platform-boundary` denies `internal/platform/**` the domain
  packages and the surfaces; `platform-boundary-root` denies a root package all of `internal/`;
  `mcp-purity`, `scheduler-purity` and `httpclient-purity` are strict allow-lists.
- **A root package carries no `mapstructure` tags and no deployment policy.** Operator knobs live
  in `internal/platform/config`, and boot maps one struct onto the other.
- **A `Service` never reaches into `Kernel`** — it takes what it needs as a constructor parameter.
