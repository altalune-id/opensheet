package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite" // sqlite driver: registers "sqlite" with database/sql
)

// Open opens the driver-specific *sql.DB, pings it with bounded retry, and applies pool tuning.
func Open(ctx context.Context, cfg DBConfig, log *slog.Logger) (*sql.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	var (
		driver string
		db     *sql.DB
		err    error
	)
	switch cfg.Driver {
	case DriverSQLite:
		driver = "sqlite"
		db, err = openSQLite(cfg)
	case DriverPostgres:
		driver = "pgx"
		db, err = openPostgres(cfg)
	default:
		return nil, fmt.Errorf("db: unknown driver %q", cfg.Driver)
	}
	if err != nil {
		return nil, err
	}

	pingErr := retry(withRetryLogger(ctx, log), cfg.ConnectTimeout, cfg.ConnectBackoff,
		func(attemptCtx context.Context) error { return db.PingContext(attemptCtx) })
	if pingErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db: ping %s: %w", driver, pingErr)
	}

	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}

	if log != nil {
		log.Debug("db opened",
			slog.String("driver", driver),
			slog.Int("max_open_conns", cfg.MaxOpenConns),
			slog.Int("max_idle_conns", cfg.MaxIdleConns),
			slog.Duration("conn_max_lifetime", cfg.ConnMaxLifetime),
			slog.Duration("conn_max_idle_time", cfg.ConnMaxIdleTime),
		)
	}

	return db, nil
}

func openSQLite(cfg DBConfig) (*sql.DB, error) {
	dsn := cfg.DSN
	if err := ensureDirFor(dsn); err != nil {
		return nil, err
	}
	dsn = sqliteDSNWithPragmas(dsn)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open sqlite: %w", err)
	}
	return db, nil
}

// NOTE: SQLite defaults foreign_keys off, so ON DELETE RESTRICT/CASCADE silently no-op without this.
func sqliteDSNWithPragmas(dsn string) string {
	pragmas := []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)"}
	if dsn == ":memory:" {
		pragmas = []string{"foreign_keys(1)"}
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	var b strings.Builder
	b.WriteString(dsn)
	for _, p := range pragmas {
		name, _, _ := strings.Cut(p, "(")
		if strings.Contains(dsn, "_pragma="+name) {
			continue
		}
		b.WriteString(sep)
		b.WriteString("_pragma=")
		b.WriteString(p)
		sep = "&"
	}
	return b.String()
}

// NOTE: BEGIN IMMEDIATE takes the write lock up front, so a unit of work that reads before it writes waits on busy_timeout instead of failing with SQLITE_BUSY_SNAPSHOT when another writer commits in between. Only the unit-of-work handle uses it: store reads open deferred transactions on W and must stay concurrent with a writer.
func sqliteDSNWithImmediateTx(dsn string) string {
	if strings.Contains(dsn, "_txlock=") {
		return dsn
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&_txlock=immediate"
	}
	return dsn + "?_txlock=immediate"
}

// NOTE: every handle on a private in-memory database opens its own empty database, so a second handle would not see W's tables.
func sqliteInMemory(dsn string) bool {
	return strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")
}

func openPostgres(cfg DBConfig) (*sql.DB, error) {
	connCfg, err := pgConnConfig(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if cfg.Role == "" {
		return stdlib.OpenDB(*connCfg, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
			registerUTCTimestamptz(conn)
			return nil
		})), nil
	}
	if err := validateRoleIdent(cfg.Role); err != nil {
		return nil, err
	}
	stmt := "SET ROLE " + quoteIdent(cfg.Role)
	afterConnect := func(ctx context.Context, conn *pgx.Conn) error {
		registerUTCTimestamptz(conn)
		_, execErr := conn.Exec(ctx, stmt)
		if execErr == nil {
			return nil
		}
		wrapped := fmt.Errorf("db: SET ROLE %q: %w", cfg.Role, execErr)
		if _, ok := errors.AsType[*pgconn.PgError](execErr); ok {
			return permanent(wrapped)
		}
		return wrapped
	}
	return stdlib.OpenDB(*connCfg, stdlib.OptionAfterConnect(afterConnect)), nil
}

func pgConnConfig(dsn string) (*pgx.ConnConfig, error) {
	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	for k := range connCfg.RuntimeParams {
		if strings.EqualFold(k, "timezone") {
			delete(connCfg.RuntimeParams, k)
		}
	}
	connCfg.RuntimeParams["timezone"] = "UTC"
	return connCfg, nil
}

func registerUTCTimestamptz(conn *pgx.Conn) {
	conn.TypeMap().RegisterType(&pgtype.Type{
		Name: "timestamptz", OID: pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	})
}

func ensureDirFor(path string) error {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("db: ensure dir %q: %w", dir, err)
	}
	return nil
}
