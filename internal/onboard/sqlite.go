package onboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type sqliteStore struct {
	db          *sql.DB
	tablePrefix string
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, tablePrefix: tablePrefix}
}

func (s *sqliteStore) table() string { return s.tablePrefix + "bootstrap" }

func (s *sqliteStore) Get(ctx context.Context) (*Bootstrap, error) {
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf("SELECT onboarded_at, onboarded_by, method FROM %s WHERE id = 1", s.table())
	var (
		atRaw  string
		byRaw  string
		method string
	)
	if err := s.db.QueryRowContext(ctx, q).Scan(&atRaw, &byRaw, &method); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &NotOnboardedError{}
		}
		return nil, fmt.Errorf("onboard.Get: %w", err)
	}
	by, err := uuid.Parse(byRaw)
	if err != nil {
		return nil, fmt.Errorf("onboard.Get: parse onboarded_by: %w", err)
	}
	at, err := parseSQLiteTime(atRaw)
	if err != nil {
		return nil, fmt.Errorf("onboard.Get: parse onboarded_at: %w", err)
	}
	return &Bootstrap{OnboardedAt: at, OnboardedBy: by, Method: Method(method)}, nil
}

func (s *sqliteStore) Save(ctx context.Context, b *Bootstrap) error {
	//nolint:gosec // G201: table identifier is fixed by config, never user input.
	q := fmt.Sprintf(`
INSERT INTO %s (id, onboarded_at, onboarded_by, method)
VALUES (1, ?, ?, ?)
ON CONFLICT(id) DO NOTHING
`, s.table())
	res, err := s.db.ExecContext(ctx, q,
		b.OnboardedAt.UTC().Format(time.RFC3339Nano),
		b.OnboardedBy.String(),
		string(b.Method),
	)
	if err != nil {
		if isSQLiteUnique(err) {
			return &AlreadyOnboardedError{}
		}
		return fmt.Errorf("onboard.Save: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("onboard.Save: rows affected: %w", err)
	}
	if n == 0 {
		return &AlreadyOnboardedError{}
	}
	return nil
}

func parseSQLiteTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unparseable time: %q", raw)
}

func isSQLiteUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed: UNIQUE")
}
