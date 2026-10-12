---
name: go-concurrency
description: Use when writing, designing or reviewing Go code that starts a goroutine, uses channels, select, sync, errgroup or context cancellation — background work, worker pools, pipelines, fan-out, long-running loops, graceful shutdown — or when coming to Go from async/await (C#/.NET Task, JavaScript Promise, Python asyncio), or chasing a goroutine leak, a hang, or a blocked channel.
license: Proprietary
---

# Go concurrency: own the lifecycle

A goroutine is a **resource with a lifetime**, like a file descriptor. The GC frees a finished
goroutine's memory, but nothing stops a running one. That is your job.

> **If you cannot say how a goroutine ends, do not start it yet.**

Builds on the `go` skill (Concurrency Patterns, Testing Patterns) — this skill is the
lifecycle half.

## Go is not async/await

| Coming from                           | In Go                                                               |
| ------------------------------------- | ------------------------------------------------------------------- |
| `async` / `await`, coloured functions | none — every function may block; write it synchronously             |
| `Task` / `Promise` returned to caller | return the **result**; the caller adds `go` if it wants concurrency |
| `CancellationToken`                   | `context.Context` as the first parameter                            |
| `Task.WhenAll`                        | `errgroup.Group` (errors) or `sync.WaitGroup` (none)                |
| fire-and-forget `Task.Run`            | `go f()` — but never bare: something must own it and wait for it    |

## Before you type `go`

Answer all of these. An answer of "it doesn't" or "not sure" is the bug.

1. **Owner** — which function starts it, and waits for it to finish?
2. **Stop** — what makes it return: work done, `ctx.Done()`, a closed channel?
3. **Caller returns** — what happens to it if the owner returns early or errors?
4. **Blocking** — can any send or receive wait forever? Is each one in a `select` with `ctx.Done()`?
5. **Data in, data out** — how do inputs arrive and results or errors leave?
6. **Shared state** — what else touches this data, and what makes that safe?
7. **Resources** — which locks, files or connections does it hold, and who releases them?

## Rules

- **The function that starts a goroutine waits for it before returning.** No goroutine outlives
  its owner's call, so APIs stay synchronous and the caller adds `go` where it wants it.
- **Start goroutines when you have concurrent work** (Bryan C. Mills). No static worker pool
  for per-item work; bound it with `errgroup.SetLimit` instead.
- **Cancelling is not owning.** `ctx` asks a goroutine to stop; the owner must also **wait**.
  `errgroup` ties start, first error, cancel and wait together — reach for it first.
- **`context.WithoutCancel` detaches cancellation, not ownership.** Work that outlives a request
  still needs an owner: a queue, an outbox, or a supervised worker drained at shutdown.
- **The sender closes a channel, never the receiver.** With several senders, none of them closes
  it — the owner closes it after they all finish (`wg.Wait()` then `close`). Closing tells the
  reader the work is done; it does not let the reader stop the sender — give the sender a `ctx` too.
- **Pass ownership of data, or guard it.** Hand a value over a channel and stop touching it, or
  keep it behind one mutex or an atomic. Never both, never neither. `-race` is the check.
- **Release in the goroutine with `defer`.** `defer mu.Unlock()`, `defer f.Close()`, `defer wg.Done()`.
- **Never hold a lock across a blocking send, a receive or a callback.** Copy what you need,
  unlock, then block. Holding it is the classic deadlock.
- **A panic in any goroutine kills the process.** The caller's `recover` cannot catch it, and
  `errgroup` does not forward it. A goroutine that runs untrusted or plugin code recovers itself
  and returns the panic as an error.
- **Isolate goroutine management.** A few places start and stop goroutines; most code is plain
  synchronous functions that know nothing about concurrency.
- **`main` is a goroutine too.** When it returns, every other goroutine dies mid-flight with no
  `defer` run. Shut down through context and wait.

## Two lifetimes

| Lifetime | Example                          | Shape                                                           |
| -------- | -------------------------------- | --------------------------------------------------------------- |
| request  | fan-out calls inside one handler | spawned per request, bounded, waited on before the response     |
| process  | poller, consumer, health loop    | fixed set, started at boot under a supervisor, stopped by `ctx` |

A process-lifetime loop started from a request handler is the classic leak. The supervisor
pattern: [`references/supervisor.md`](references/supervisor.md).

## Shape

```go
func FetchAll(ctx context.Context, urls []string) ([]Result, error) {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	out := make([]Result, len(urls))
	for i, u := range urls {
		g.Go(func() error {
			r, err := fetch(ctx, u)
			out[i] = r
			return err
		})
	}
	return out, g.Wait()
}
```

Owner: `FetchAll`. Stop: work done or `ctx` cancelled by the first error. Data: each goroutine
writes only its own slice index — safe. Separate **map** keys are not: concurrent map writes crash
the process. Neither is a captured outer `err`. Nothing outlives the call.

## Red flags

| Seen                                        | Problem                                                                                                                                              |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go` with no `Wait`, errgroup or supervisor | nobody owns it                                                                                                                                       |
| a bare `ch <- v` or `<-ch` in a goroutine   | blocks forever once the other side leaves                                                                                                            |
| `time.Sleep` in a loop                      | ignores cancellation — use a `time.Ticker` + `select`                                                                                                |
| `go` inside a handler for "later" work      | outlives the request; use a queue or outbox                                                                                                          |
| a function returning a channel it feeds     | its goroutine outlives the call — prefer an iterator or callback; if a channel is unavoidable, take `ctx` and document that the caller must drain it |
| `wg.Add` inside the goroutine               | races with `Wait` — use `wg.Go` or `Add` before `go`                                                                                                 |

## Testing

- Every test runs with `-race`.
- Test concurrent code with `testing/synctest` (Go 1.25): fake time, and `synctest.Test` fails
  as a deadlock when every goroutine it started is durably blocked (bubble channels, `select`,
  `WaitGroup`, timers). A goroutine stuck on a mutex, I/O or an outside channel hangs instead.
- Cancel the context in a test and assert the call returns — a hang is a leak.

## Reference

Bryan C. Mills, _Rethinking Classical Concurrency Patterns_, GopherCon 2018 —
https://www.youtube.com/watch?v=5zXAHh5tJqQ
