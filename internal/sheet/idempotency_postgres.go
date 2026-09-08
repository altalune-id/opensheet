package sheet

import (
	"context"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresIdempotencyStore struct {
	pgTxn

	table *pgent.SheetWriteAttempts
}

func newPostgresIdempotencyStore(pc *tenant.PgConn, schema, tablePrefix string) *postgresIdempotencyStore {
	return &postgresIdempotencyStore{
		pgTxn: pgTxn{pc: pc},
		table: pgent.NewSheetWriteAttempts(schema, tablePrefix),
	}
}

type pgAttemptRow struct {
	BodyHash   string    `alias:"sheet_write_attempts.body_hash"`
	Payload    []byte    `alias:"sheet_write_attempts.payload"`
	Done       bool      `alias:"sheet_write_attempts.done"`
	ClaimToken uuid.UUID `alias:"sheet_write_attempts.claim_token"`
	CreatedAt  time.Time `alias:"sheet_write_attempts.created_at"`
}

func (r *pgAttemptRow) toAttempt() Attempt {
	return Attempt{
		BodyHash:  r.BodyHash,
		Payload:   r.Payload,
		Done:      r.Done,
		CreatedAt: r.CreatedAt.UTC(),
	}
}

func (s *postgresIdempotencyStore) Reserve(
	ctx context.Context, k IdempotencyKey, bodyHash string, ttl time.Duration,
) (bool, Attempt, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, Attempt{}, err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return false, Attempt{}, err
	}

	token := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	// NOTE: the DO UPDATE is a deliberate no-op — it exists only so RETURNING yields the conflicting row, which DO NOTHING would not.
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			k.SheetID, k.Tab, k.Key, bodyHash, postgres.NULL, false, token,
			now, now.Add(ttl), tc.OrgID, tc.ProjectID,
		).
		ON_CONFLICT(s.table.SheetID, s.table.Tab, s.table.IdemKey).
		DO_UPDATE(postgres.SET(s.table.IdemKey.SET(postgres.String(k.Key)))).
		RETURNING(
			s.table.BodyHash, s.table.Payload, s.table.Done,
			s.table.ClaimToken, s.table.CreatedAt,
		)

	var row pgAttemptRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		return false, Attempt{}, s.endTx(tx, owned, fmt.Errorf("sheet.idempotency.postgres.Reserve: %w", qErr))
	}
	if cErr := s.endTx(tx, owned, nil); cErr != nil {
		return false, Attempt{}, cErr
	}
	return row.ClaimToken == token, row.toAttempt(), nil
}

func (s *postgresIdempotencyStore) Complete(
	ctx context.Context, k IdempotencyKey, payload []byte, ttl time.Duration,
) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.table.UPDATE(s.table.Payload, s.table.Done, s.table.ExpiresAt).
		SET(postgres.Bytea(payload), postgres.Bool(true), postgres.TimestampzT(now.Add(ttl))).
		WHERE(s.keyEq(k))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.idempotency.postgres.Complete: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresIdempotencyStore) Release(ctx context.Context, k IdempotencyKey) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().WHERE(s.keyEq(k))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.idempotency.postgres.Release: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresIdempotencyStore) DeleteExpired(ctx context.Context) (int, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return 0, err
	}
	stmt := s.table.DELETE().
		WHERE(s.table.ExpiresAt.LT_EQ(postgres.TimestampzT(time.Now().UTC())))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return 0, s.endTx(tx, owned, fmt.Errorf("sheet.idempotency.postgres.DeleteExpired: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return 0, s.endTx(tx, owned, fmt.Errorf("sheet.idempotency.postgres.DeleteExpired: rows affected: %w", raErr))
	}
	if cErr := s.endTx(tx, owned, nil); cErr != nil {
		return 0, cErr
	}
	return int(n), nil
}

func (s *postgresIdempotencyStore) keyEq(k IdempotencyKey) postgres.BoolExpression {
	return s.table.SheetID.EQ(postgres.UUID(k.SheetID)).
		AND(s.table.Tab.EQ(postgres.String(k.Tab))).
		AND(s.table.IdemKey.EQ(postgres.String(k.Key)))
}
