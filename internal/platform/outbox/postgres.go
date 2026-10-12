package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresStore struct {
	pool    pdb.Pool
	pc      *tenant.PgConn
	entries *pgent.OutboxEntries
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, entries: pgent.NewOutboxEntries(schema, tablePrefix)}
}

type pgEntryRow struct {
	ID            uuid.UUID `alias:"outbox_entries.id"`
	EventID       uuid.UUID `alias:"outbox_entries.event_id"`
	OrgID         uuid.UUID `alias:"outbox_entries.org_id"`
	ProjectID     uuid.UUID `alias:"outbox_entries.project_id"`
	Target        string    `alias:"outbox_entries.target"`
	Payload       []byte    `alias:"outbox_entries.payload"`
	Attempt       int32     `alias:"outbox_entries.attempt"`
	NextAttemptAt time.Time `alias:"outbox_entries.next_attempt_at"`
	Status        string    `alias:"outbox_entries.status"`
	LastError     string    `alias:"outbox_entries.last_error"`
	CreatedAt     time.Time `alias:"outbox_entries.created_at"`
}

func (r *pgEntryRow) toEntry() Entry {
	return Entry{
		ID:            r.ID,
		EventID:       r.EventID,
		OrgID:         r.OrgID,
		ProjectID:     r.ProjectID,
		Target:        r.Target,
		Payload:       r.Payload,
		Attempt:       int(r.Attempt),
		NextAttemptAt: r.NextAttemptAt.UTC(),
		Status:        Status(r.Status),
		LastError:     r.LastError,
		CreatedAt:     r.CreatedAt.UTC(),
	}
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, tc, nil
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("outbox.postgres: begin: %w", err)
	}
	return tx, true, tc, nil
}

func (s *postgresStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("outbox.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) Enqueue(ctx context.Context, e Entry) error {
	if err := e.validate(); err != nil {
		return err
	}
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.enqueue(ctx, tx, tc, e))
}

func (s *postgresStore) enqueue(ctx context.Context, tx *sql.Tx, tc tenant.Context, e Entry) error {
	if e.OrgID != tc.OrgID {
		// SECURITY: an entry naming another org would be delivered under that org's scope.
		return &InvalidEntryError{Field: "OrgID", Reason: "does not match the tenant scope on ctx"}
	}
	if e.ProjectID != tc.ProjectID {
		// SECURITY: the project FK is checked as the table owner and bypasses RLS, so only this
		// comparison stops an entry carrying another org's project.
		return &InvalidEntryError{Field: "ProjectID", Reason: "does not match the tenant scope on ctx"}
	}
	now := time.Now().UTC()
	due := e.NextAttemptAt
	if due.IsZero() {
		due = now
	}
	stmt := s.entries.INSERT(s.entries.AllColumns).
		VALUES(
			e.ID, e.EventID, e.OrgID, e.ProjectID, e.Target, e.Payload,
			0, due.UTC(), string(StatusPending), "", now, pgent.NullTimestampz(),
		).
		ON_CONFLICT(s.entries.OrgID, s.entries.EventID, s.entries.Target).
		// NOTE: a redelivered domain event must not become a second delivery.
		DO_NOTHING()
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return fmt.Errorf("outbox.postgres.Enqueue: %w", execErr)
	}
	return nil
}

func (s *postgresStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Entry, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	claimed, claimErr := s.claimDue(ctx, tx, tc, now, limit)
	if txErr := s.endTx(tx, owned, claimErr); txErr != nil {
		return nil, txErr
	}
	return claimed, nil
}

func (s *postgresStore) claimDue(ctx context.Context, tx *sql.Tx, tc tenant.Context, now time.Time, limit int) ([]Entry, error) {
	if err := s.reapExhausted(ctx, tx, tc, now); err != nil {
		return nil, err
	}
	// SECURITY/correctness: SKIP LOCKED is what stops two replicas claiming one entry and
	// delivering the same event twice on every tick.
	due := postgres.SELECT(s.entries.ID).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.entries.Status.EQ(postgres.String(string(StatusPending)))).
			AND(s.entries.Attempt.LT(postgres.Int(MaxAttempts))).
			AND(s.entries.NextAttemptAt.LT_EQ(postgres.TimestampzT(now.UTC())))).
		ORDER_BY(s.entries.NextAttemptAt.ASC()).
		LIMIT(claimBatch(limit)).
		FOR(postgres.UPDATE().SKIP_LOCKED())

	stmt := s.entries.UPDATE(s.entries.Attempt, s.entries.NextAttemptAt).
		SET(
			s.entries.Attempt.ADD(postgres.Int(1)),
			postgres.TimestampzT(now.UTC().Add(ClaimLease)),
		).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.entries.ID.IN(due))).
		RETURNING(s.entries.AllColumns)

	var rows []pgEntryRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil && !errorIsNoRows(err) {
		return nil, fmt.Errorf("outbox.postgres.ClaimDue: %w", err)
	}
	out := make([]Entry, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toEntry())
	}
	return out, nil
}

// NOTE: without it a dispatcher that dies mid-flight every time strands the row in pending; the lease guard is what keeps it off an entry whose final attempt is still being delivered.
func (s *postgresStore) reapExhausted(ctx context.Context, tx *sql.Tx, tc tenant.Context, now time.Time) error {
	stmt := s.entries.UPDATE(s.entries.Status).
		SET(postgres.String(string(StatusFailed))).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.entries.Status.EQ(postgres.String(string(StatusPending)))).
			AND(s.entries.Attempt.GT_EQ(postgres.Int(MaxAttempts))).
			AND(s.entries.NextAttemptAt.LT_EQ(postgres.TimestampzT(now.UTC()))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("outbox.postgres.ClaimDue: reap exhausted: %w", err)
	}
	return nil
}

func (s *postgresStore) Succeed(ctx context.Context, e Entry, at time.Time) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.DeliveredAt, s.entries.LastError).
		SET(postgres.String(string(StatusDelivered)), postgres.TimestampzT(at.UTC()), postgres.String("")).
		WHERE(s.claimedRow(tc, e))
	return s.endTx(tx, owned, s.settle(ctx, tx, tc, e, stmt))
}

func (s *postgresStore) Fail(ctx context.Context, e Entry, retryAt time.Time, cause string) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	nextStatus := postgres.CASE().
		WHEN(s.entries.Attempt.GT_EQ(postgres.Int(MaxAttempts))).
		THEN(postgres.String(string(StatusFailed))).
		ELSE(postgres.String(string(StatusPending)))
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.NextAttemptAt, s.entries.LastError).
		SET(nextStatus, postgres.TimestampzT(retryAt.UTC()), postgres.String(truncateCause(cause))).
		WHERE(s.claimedRow(tc, e))
	return s.endTx(tx, owned, s.settle(ctx, tx, tc, e, stmt))
}

// SECURITY/correctness: the attempt fences the settle, so a dispatcher whose lease expired cannot overwrite the lease of whoever re-claimed the entry.
func (s *postgresStore) claimedRow(tc tenant.Context, e Entry) postgres.BoolExpression {
	return s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
		AND(s.entries.ID.EQ(postgres.UUID(e.ID))).
		AND(s.entries.Status.EQ(postgres.String(string(StatusPending)))).
		AND(s.entries.Attempt.EQ(postgres.Int(int64(e.Attempt))))
}

type pgExecutable interface {
	ExecContext(ctx context.Context, db qrm.Executable) (sql.Result, error)
}

func (s *postgresStore) settle(ctx context.Context, tx *sql.Tx, tc tenant.Context, e Entry, stmt pgExecutable) error {
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("outbox.postgres: settle: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("outbox.postgres: settle: rows affected: %w", raErr)
	}
	if n > 0 {
		return nil
	}
	return s.refuseReason(ctx, tx, tc, e)
}

func (s *postgresStore) refuseReason(ctx context.Context, tx *sql.Tx, tc tenant.Context, e Entry) error {
	stmt := postgres.SELECT(s.entries.Status, s.entries.Attempt).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.entries.ID.EQ(postgres.UUID(e.ID))))
	var row struct {
		Status  string `alias:"outbox_entries.status"`
		Attempt int32  `alias:"outbox_entries.attempt"`
	}
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errorIsNoRows(err) {
			return &NotFoundError{ID: e.ID.String()}
		}
		return fmt.Errorf("outbox.postgres: settle: read status: %w", err)
	}
	if Status(row.Status) == StatusPending {
		return &StaleClaimError{ID: e.ID.String(), Claimed: e.Attempt, Current: int(row.Attempt)}
	}
	return &TerminalStateError{ID: e.ID.String(), Status: Status(row.Status)}
}

func (s *postgresStore) Requeue(ctx context.Context, id uuid.UUID, target string) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.requeue(ctx, tx, tc, id, target))
}

func (s *postgresStore) requeue(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, target string) error {
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.Attempt, s.entries.NextAttemptAt, s.entries.LastError).
		SET(postgres.String(string(StatusPending)), postgres.Int(0), postgres.TimestampzT(time.Now().UTC()), postgres.String("")).
		WHERE(s.failedRow(tc, id, target))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("outbox.postgres.Requeue: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("outbox.postgres.Requeue: rows affected: %w", raErr)
	}
	if n > 0 {
		return nil
	}
	return s.refuseRequeue(ctx, tx, tc, id, target)
}

func (s *postgresStore) failedRow(tc tenant.Context, id uuid.UUID, target string) postgres.BoolExpression {
	return s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
		AND(s.entries.ID.EQ(postgres.UUID(id))).
		AND(s.entries.Target.EQ(postgres.String(target))).
		AND(s.entries.Status.EQ(postgres.String(string(StatusFailed))))
}

func (s *postgresStore) refuseRequeue(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, target string) error {
	stmt := postgres.SELECT(s.entries.Status).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.entries.ID.EQ(postgres.UUID(id))).
			AND(s.entries.Target.EQ(postgres.String(target))))
	var row struct {
		Status string `alias:"outbox_entries.status"`
	}
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errorIsNoRows(err) {
			return &NotFoundError{ID: id.String()}
		}
		return fmt.Errorf("outbox.postgres.Requeue: read status: %w", err)
	}
	return &NotFailedError{ID: id.String(), Status: Status(row.Status)}
}

func (s *postgresStore) RequeueFailed(ctx context.Context, target string) (int, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return 0, err
	}
	n, reqErr := s.requeueFailed(ctx, tx, tc, target)
	if txErr := s.endTx(tx, owned, reqErr); txErr != nil {
		return 0, txErr
	}
	return n, nil
}

func (s *postgresStore) requeueFailed(ctx context.Context, tx *sql.Tx, tc tenant.Context, target string) (int, error) {
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.Attempt, s.entries.NextAttemptAt, s.entries.LastError).
		SET(postgres.String(string(StatusPending)), postgres.Int(0), postgres.TimestampzT(time.Now().UTC()), postgres.String("")).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.entries.Target.EQ(postgres.String(target))).
			AND(s.entries.Status.EQ(postgres.String(string(StatusFailed)))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("outbox.postgres.RequeueFailed: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return 0, fmt.Errorf("outbox.postgres.RequeueFailed: rows affected: %w", raErr)
	}
	return int(n), nil
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID, target string) (Entry, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return Entry{}, err
	}
	e, readErr := s.byID(ctx, tx, tc, id, target)
	if txErr := s.endTx(tx, owned, readErr); txErr != nil {
		return Entry{}, txErr
	}
	return e, nil
}

func (s *postgresStore) byID(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, target string) (Entry, error) {
	stmt := postgres.SELECT(s.entries.AllColumns).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.entries.ID.EQ(postgres.UUID(id))).
			AND(s.entries.Target.EQ(postgres.String(target))))

	var row pgEntryRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errorIsNoRows(err) {
			return Entry{}, &NotFoundError{ID: id.String()}
		}
		return Entry{}, fmt.Errorf("outbox.postgres.ByID: %w", err)
	}
	return row.toEntry(), nil
}

func (s *postgresStore) ListByTarget(ctx context.Context, target string, limit int) ([]Entry, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	out, listErr := s.listByTarget(ctx, tx, tc, target, limit)
	if txErr := s.endTx(tx, owned, listErr); txErr != nil {
		return nil, txErr
	}
	return out, nil
}

func (s *postgresStore) listByTarget(ctx context.Context, tx *sql.Tx, tc tenant.Context, target string, limit int) ([]Entry, error) {
	stmt := postgres.SELECT(s.entries.AllColumns).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.entries.Target.EQ(postgres.String(target)))).
		ORDER_BY(s.entries.CreatedAt.DESC(), s.entries.ID.DESC()).
		LIMIT(listBatch(limit))

	var rows []pgEntryRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil && !errorIsNoRows(err) {
		return nil, fmt.Errorf("outbox.postgres.ListByTarget: %w", err)
	}
	out := make([]Entry, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toEntry())
	}
	return out, nil
}
