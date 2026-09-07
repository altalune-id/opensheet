package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"altalune.id/template/scheduler"
)

const unlockBudget = 5 * time.Second

var (
	_ scheduler.Locker = NoopLocker{}
	_ scheduler.Locker = (*PgLocker)(nil)
)

// LockKey derives the advisory-lock key for a job name.
func LockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64()) //nolint:gosec // G115: wrapping to int64 is intentional; pg_advisory_lock takes a signed bigint.
}

// NoopLocker always acquires; SQLite has a single writing process.
type NoopLocker struct{}

// TryLock implements scheduler.Locker.
func (NoopLocker) TryLock(context.Context, string) (release func(), acquired bool, err error) {
	return func() {}, true, nil
}

// PgLocker implements scheduler.Locker with session-scoped Postgres advisory locks.
type PgLocker struct {
	pool Pool
	log  *slog.Logger
}

// NewPgLocker builds the Postgres leader lock over pool's writer handle.
func NewPgLocker(pool Pool, log *slog.Logger) *PgLocker { return &PgLocker{pool: pool, log: log} }

// NewLocker returns the driver-appropriate scheduler.Locker.
func NewLocker(cfg DBConfig, pool Pool, log *slog.Logger) scheduler.Locker {
	if cfg.Driver == DriverPostgres {
		return NewPgLocker(pool, log)
	}
	return NoopLocker{}
}

// TryLock implements scheduler.Locker. NOTE: an advisory lock is session-scoped, so it is held on one pinned connection, never the pool.
func (l *PgLocker) TryLock(ctx context.Context, name string) (release func(), acquired bool, err error) {
	key := LockKey(name)
	conn, err := l.pool.W.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("db: locker acquire conn: %w", err)
	}

	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("db: pg_try_advisory_lock(%q): %w", name, err)
	}
	if !got {
		_ = conn.Close()
		return nil, false, nil
	}

	return func() { l.release(ctx, conn, name, key) }, true, nil
}

// NOTE: unlocks on a detached ctx — the job ctx is already canceled in the timeout case this exists for.
func (l *PgLocker) release(jobCtx context.Context, conn *sql.Conn, name string, key int64) {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(jobCtx), unlockBudget)
	defer cancel()

	var released bool
	err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&released)
	if err != nil || !released {
		if l.log != nil {
			l.log.ErrorContext(unlockCtx, "db: advisory unlock failed — discarding connection",
				slog.String("job", name), slog.Bool("released", released), slog.Any("err", err))
		}
		// SECURITY: a pooled session still holding the lock would deadlock the job on every replica forever.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}
