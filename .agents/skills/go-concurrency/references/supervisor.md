# Supervisor: owning process-lifetime loops

A poller, a queue consumer or a health loop lives as long as the process. Give all of them
**one owner**: a supervisor that starts them at boot, cancels them together, and waits for
every one to return before `main` exits.

## The contract

```go
type Worker interface {
	Name() string
	Run(ctx context.Context) error
}
```

- `Run` blocks until `ctx` is cancelled or the worker fails. It never starts a goroutine it
  does not wait for.
- **Shutdown returns `nil`**, never `ctx.Err()`. Cancellation is the normal way to stop.
- **A non-nil error means unrecoverable.** It cancels every sibling and ends the process.
- **A periodic loop logs a per-tick failure and keeps going.** A blip must not kill the process.
- **Stop within a budget** (for example 10 s). A `Run` that ignores `ctx` blocks shutdown.

## The supervisor

```go
type Supervisor struct{ workers []Worker }

func (s *Supervisor) Register(w Worker) { s.workers = append(s.workers, w) }

func (s *Supervisor) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, w := range s.workers {
		g.Go(func() error {
			if err := w.Run(ctx); err != nil {
				return fmt.Errorf("%s: %w", w.Name(), err)
			}
			return nil
		})
	}
	return g.Wait()
}
```

## A periodic worker

```go
func (p *Poller) Run(ctx context.Context) error {
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := p.tick(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("poll failed", "err", err)
			}
		}
	}
}
```

## Boot

```go
func main() {
	if err := run(); err != nil {
		slog.Error("stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var sup Supervisor
	sup.Register(httpWorker)
	sup.Register(poller)
	return sup.Run(ctx)
}
```

## The HTTP server is a worker too

It owns its `ListenAndServe` goroutine and does not return until that goroutine has.

```go
func (h *HTTP) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- h.srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	shutdownErr := h.srv.Shutdown(sctx)
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
```

## Choosing

| Need                                           | Use                                             |
| ---------------------------------------------- | ----------------------------------------------- |
| every replica runs its own loop                | a supervised worker                             |
| one replica per tick, or run on demand by name | a scheduler job with a lock or leader           |
| work that must survive a crash or a restart    | a durable queue or outbox, consumed by a worker |
