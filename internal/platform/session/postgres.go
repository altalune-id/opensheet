package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"

	"altalune.id/opensheet/internal/apperror"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/sealer"
)

const deleteExpiredBatch = 500

type pgStore struct {
	db         *sql.DB
	table      *pgent.Sessions
	sealer     sealer.Sealer
	unexpected apperror.UnexpectedFunc
}

func (s *pgStore) Save(ctx context.Context, sid string, p Principal, exp time.Time) error {
	blob, err := sealPrincipal(s.sealer, sid, p)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.table.INSERT(
		s.table.SID,
		s.table.UserID,
		s.table.Payload,
		s.table.ExpiresAt,
		s.table.CreatedAt,
	).
		VALUES(sid, p.UserID, blob, exp.UTC(), now).
		ON_CONFLICT(s.table.SID).
		DO_UPDATE(postgres.SET(
			s.table.Payload.SET(postgres.Bytea(blob)),
			s.table.ExpiresAt.SET(postgres.TimestampzT(exp.UTC())),
		))
	if _, execErr := stmt.ExecContext(ctx, s.db); execErr != nil {
		return fmt.Errorf("session.postgres.Save: %w", execErr)
	}
	return nil
}

func (s *pgStore) Load(ctx context.Context, sid string) (Principal, bool, error) {
	stmt := postgres.SELECT(s.table.Payload).
		FROM(s.table).
		WHERE(
			s.table.SID.EQ(postgres.String(sid)).
				AND(s.table.ExpiresAt.GT(postgres.NOW())),
		).
		LIMIT(1)

	var row struct {
		Payload []byte `alias:"sessions.payload"`
	}
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return Principal{}, false, nil
		}
		return Principal{}, false, fmt.Errorf("session.postgres.Load: %w", err)
	}

	p, oErr := openPrincipal(s.sealer, sid, row.Payload)
	if oErr != nil {
		// SECURITY: a rotated key or a changed Principal shape logs the holder out rather than 500s them; a systemic failure logs out everyone, so it is reported.
		_ = s.unexpected(ctx, "session: open principal", oErr)
		return Principal{}, false, nil //nolint:nilerr // an unopenable session is "not signed in", not a request failure.
	}
	return p, true, nil
}

func (s *pgStore) Delete(ctx context.Context, sid string) error {
	stmt := s.table.DELETE().WHERE(s.table.SID.EQ(postgres.String(sid)))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("session.postgres.Delete: %w", err)
	}
	return nil
}

func (s *pgStore) DeleteExpired(ctx context.Context) (int, error) {
	total := 0
	for {
		expired := postgres.SELECT(s.table.SID).
			FROM(s.table).
			WHERE(s.table.ExpiresAt.LT_EQ(postgres.NOW())).
			LIMIT(deleteExpiredBatch)

		stmt := s.table.DELETE().WHERE(s.table.SID.IN(expired))
		res, err := stmt.ExecContext(ctx, s.db)
		if err != nil {
			return total, fmt.Errorf("session.postgres.DeleteExpired: %w", err)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return total, fmt.Errorf("session.postgres.DeleteExpired: rows affected: %w", raErr)
		}
		total += int(n)
		if int(n) < deleteExpiredBatch {
			return total, nil
		}
	}
}
