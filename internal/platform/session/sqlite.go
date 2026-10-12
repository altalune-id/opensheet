package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"

	"altalune.id/opensheet/internal/apperror"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/sealer"
)

type sqliteStore struct {
	db         *sql.DB
	table      *sqliteent.Sessions
	sealer     sealer.Sealer
	unexpected apperror.UnexpectedFunc
}

func (s *sqliteStore) Save(ctx context.Context, sid string, p Principal, exp time.Time) error {
	blob, err := sealPrincipal(s.sealer, sid, p)
	if err != nil {
		return err
	}
	expiresAt := sqliteent.SQLiteTime(exp)
	// NOTE: UPSERT, not INSERT — live call sites re-Save under an existing sid on every org switch.
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(sid, p.UserID.String(), blob, expiresAt, sqliteent.SQLiteTime(time.Now())).
		ON_CONFLICT(s.table.SID).
		DO_UPDATE(sqlite.SET(
			s.table.Payload.SET(sqlite.Blob(blob)),
			s.table.ExpiresAt.SET(sqlite.String(expiresAt)),
		))
	if _, execErr := stmt.ExecContext(ctx, s.db); execErr != nil {
		return fmt.Errorf("session.sqlite.Save: %w", execErr)
	}
	return nil
}

func (s *sqliteStore) Load(ctx context.Context, sid string) (Principal, bool, error) {
	stmt := sqlite.SELECT(s.table.Payload).
		FROM(s.table).
		WHERE(
			s.table.SID.EQ(sqlite.String(sid)).
				AND(s.table.ExpiresAt.GT(sqlite.String(sqliteent.SQLiteTime(time.Now())))),
		).
		LIMIT(1)

	var row struct {
		Payload []byte `alias:"sessions.payload"`
	}
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return Principal{}, false, nil
		}
		return Principal{}, false, fmt.Errorf("session.sqlite.Load: %w", err)
	}

	p, oErr := openPrincipal(s.sealer, sid, row.Payload)
	if oErr != nil {
		// SECURITY: a rotated key or a changed Principal shape logs the holder out rather than 500s them; a systemic failure logs out everyone, so it is reported.
		_ = s.unexpected(ctx, "session: open principal", oErr)
		return Principal{}, false, nil //nolint:nilerr // an unopenable session is "not signed in", not a request failure.
	}
	return p, true, nil
}

func (s *sqliteStore) Delete(ctx context.Context, sid string) error {
	stmt := s.table.DELETE().WHERE(s.table.SID.EQ(sqlite.String(sid)))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("session.sqlite.Delete: %w", err)
	}
	return nil
}

func (s *sqliteStore) DeleteExpired(ctx context.Context) (int, error) {
	stmt := s.table.DELETE().
		WHERE(s.table.ExpiresAt.LT_EQ(sqlite.String(sqliteent.SQLiteTime(time.Now()))))
	res, err := stmt.ExecContext(ctx, s.db)
	if err != nil {
		return 0, fmt.Errorf("session.sqlite.DeleteExpired: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return 0, fmt.Errorf("session.sqlite.DeleteExpired: rows affected: %w", raErr)
	}
	return int(n), nil
}
