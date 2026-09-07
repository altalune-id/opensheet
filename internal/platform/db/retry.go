package db

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const maxConnectBackoff = 5 * time.Second

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }

func (e *permanentError) Unwrap() error { return e.err }

func permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

func isPermanentError(err error) bool {
	_, ok := errors.AsType[*permanentError](err)
	return ok
}

func attemptWithin(ctx context.Context, remaining time.Duration, attempt func(context.Context) error) error {
	if remaining <= 0 {
		return attempt(ctx)
	}
	attemptCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	return attempt(attemptCtx)
}

func retry(ctx context.Context, budget, backoff time.Duration, attempt func(context.Context) error) error {
	if backoff <= 0 {
		backoff = 250 * time.Millisecond
	}
	start := time.Now()
	var lastErr error
	for n := 1; ; n++ {
		remaining := budget - time.Since(start)
		if remaining <= 0 && lastErr != nil {
			return lastErr
		}
		err := attemptWithin(ctx, remaining, attempt)
		if err == nil {
			return nil
		}
		if isPermanentError(err) {
			return err
		}
		lastErr = err
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		remaining = budget - time.Since(start)
		if remaining <= 0 {
			return err
		}
		sleep := min(backoff, remaining)
		if log := loggerFrom(ctx); log != nil {
			log.WarnContext(ctx, "db: connect attempt failed, retrying",
				slog.Int("attempt", n),
				slog.Duration("elapsed", time.Since(start)),
				slog.Duration("backoff", sleep),
				slog.String("err", err.Error()),
			)
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, maxConnectBackoff)
	}
}

type retryLogKey struct{}

func withRetryLogger(ctx context.Context, log *slog.Logger) context.Context {
	if log == nil {
		return ctx
	}
	return context.WithValue(ctx, retryLogKey{}, log)
}

func loggerFrom(ctx context.Context) *slog.Logger {
	log, _ := ctx.Value(retryLogKey{}).(*slog.Logger)
	return log
}
