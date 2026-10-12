package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteConn interface {
	qrm.Queryable
	qrm.Executable
}

type sqliteStore struct {
	db        *sql.DB
	entries   *sqliteent.OutboxEntries
	returning []sqlite.Projection
}

func newSQLiteStore(sqlDB *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:        sqlDB,
		entries:   sqliteent.NewOutboxEntries(tablePrefix),
		returning: claimReturning(),
	}
}

// NOTE: SQLite rejects a table-qualified name in RETURNING, so the projections are free-standing columns.
func claimReturning() []sqlite.Projection {
	return []sqlite.Projection{
		sqlite.StringColumn("id").AS("outbox_entries.id"),
		sqlite.StringColumn("event_id").AS("outbox_entries.event_id"),
		sqlite.StringColumn("org_id").AS("outbox_entries.org_id"),
		sqlite.StringColumn("project_id").AS("outbox_entries.project_id"),
		sqlite.StringColumn("target").AS("outbox_entries.target"),
		sqlite.BlobColumn("payload").AS("outbox_entries.payload"),
		sqlite.IntegerColumn("attempt").AS("outbox_entries.attempt"),
		sqlite.StringColumn("next_attempt_at").AS("outbox_entries.next_attempt_at"),
		sqlite.StringColumn("status").AS("outbox_entries.status"),
		sqlite.StringColumn("last_error").AS("outbox_entries.last_error"),
		sqlite.StringColumn("created_at").AS("outbox_entries.created_at"),
	}
}

// NOTE: runs standalone outside a unit of work, so SQLite never upgrades a stale read snapshot into a write.
func (s *sqliteStore) conn(ctx context.Context) (sqliteConn, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, tc, nil
	}
	return s.db, tc, nil
}

type sqliteEntryRow struct {
	ID            string `alias:"outbox_entries.id"`
	EventID       string `alias:"outbox_entries.event_id"`
	OrgID         string `alias:"outbox_entries.org_id"`
	ProjectID     string `alias:"outbox_entries.project_id"`
	Target        string `alias:"outbox_entries.target"`
	Payload       []byte `alias:"outbox_entries.payload"`
	Attempt       int32  `alias:"outbox_entries.attempt"`
	NextAttemptAt string `alias:"outbox_entries.next_attempt_at"`
	Status        string `alias:"outbox_entries.status"`
	LastError     string `alias:"outbox_entries.last_error"`
	CreatedAt     string `alias:"outbox_entries.created_at"`
}

func (r *sqliteEntryRow) toEntry() (Entry, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox.sqlite: parse id: %w", err)
	}
	eventID, err := uuid.Parse(r.EventID)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox.sqlite: parse event_id: %w", err)
	}
	orgID, err := uuid.Parse(r.OrgID)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox.sqlite: parse org_id: %w", err)
	}
	projectID, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox.sqlite: parse project_id: %w", err)
	}
	due, err := time.Parse(time.RFC3339Nano, r.NextAttemptAt)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox.sqlite: parse next_attempt_at: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox.sqlite: parse created_at: %w", err)
	}
	return Entry{
		ID:            id,
		EventID:       eventID,
		OrgID:         orgID,
		ProjectID:     projectID,
		Target:        r.Target,
		Payload:       r.Payload,
		Attempt:       int(r.Attempt),
		NextAttemptAt: due.UTC(),
		Status:        Status(r.Status),
		LastError:     r.LastError,
		CreatedAt:     createdAt.UTC(),
	}, nil
}

func (s *sqliteStore) Enqueue(ctx context.Context, e Entry) error {
	if err := e.validate(); err != nil {
		return err
	}
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return err
	}
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
			e.ID.String(), e.EventID.String(), e.OrgID.String(), e.ProjectID.String(),
			e.Target, e.Payload, 0, sqliteent.SQLiteTime(due), string(StatusPending), "",
			sqliteent.SQLiteTime(now), sqliteent.NullText(),
		).
		ON_CONFLICT(s.entries.OrgID, s.entries.EventID, s.entries.Target).
		// NOTE: a redelivered domain event must not become a second delivery.
		DO_NOTHING()
	if _, execErr := stmt.ExecContext(ctx, conn); execErr != nil {
		return fmt.Errorf("outbox.sqlite.Enqueue: %w", execErr)
	}
	return nil
}

func (s *sqliteStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Entry, error) {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}
	if reapErr := s.reapExhausted(ctx, conn, tc, now); reapErr != nil {
		return nil, reapErr
	}
	due := sqlite.SELECT(s.entries.ID).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.Status.EQ(sqlite.String(string(StatusPending)))).
			AND(s.entries.Attempt.LT(sqlite.Int(MaxAttempts))).
			AND(s.entries.NextAttemptAt.LT_EQ(sqlite.String(sqliteent.SQLiteTime(now))))).
		ORDER_BY(s.entries.NextAttemptAt.ASC()).
		LIMIT(claimBatch(limit))

	// SECURITY: one statement, so SQLite's single writer serializes the claim.
	stmt := s.entries.UPDATE(s.entries.Attempt, s.entries.NextAttemptAt).
		SET(
			s.entries.Attempt.ADD(sqlite.Int(1)),
			sqlite.String(sqliteent.SQLiteTime(now.Add(ClaimLease))),
		).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).AND(s.entries.ID.IN(due))).
		RETURNING(s.returning...)

	var rows []sqliteEntryRow
	if qErr := stmt.QueryContext(ctx, conn, &rows); qErr != nil && !errorIsNoRows(qErr) {
		return nil, fmt.Errorf("outbox.sqlite.ClaimDue: %w", qErr)
	}
	out := make([]Entry, 0, len(rows))
	for i := range rows {
		e, cErr := rows[i].toEntry()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, e)
	}
	return out, nil
}

// NOTE: without this a dispatcher that dies mid-flight every time strands the row in pending; the lease guard is what keeps it off an entry whose final attempt is still being delivered.
func (s *sqliteStore) reapExhausted(ctx context.Context, conn sqliteConn, tc tenant.Context, now time.Time) error {
	stmt := s.entries.UPDATE(s.entries.Status).
		SET(sqlite.String(string(StatusFailed))).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.Status.EQ(sqlite.String(string(StatusPending)))).
			AND(s.entries.Attempt.GT_EQ(sqlite.Int(MaxAttempts))).
			AND(s.entries.NextAttemptAt.LT_EQ(sqlite.String(sqliteent.SQLiteTime(now)))))
	if _, err := stmt.ExecContext(ctx, conn); err != nil {
		return fmt.Errorf("outbox.sqlite.ClaimDue: reap exhausted: %w", err)
	}
	return nil
}

func (s *sqliteStore) Succeed(ctx context.Context, e Entry, at time.Time) error {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return err
	}
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.DeliveredAt, s.entries.LastError).
		SET(sqlite.String(string(StatusDelivered)), sqlite.String(sqliteent.SQLiteTime(at)), sqlite.String("")).
		WHERE(s.claimedRow(tc, e))
	return s.settle(ctx, conn, tc, e, stmt)
}

func (s *sqliteStore) Fail(ctx context.Context, e Entry, retryAt time.Time, cause string) error {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return err
	}
	nextStatus := sqlite.CASE().
		WHEN(s.entries.Attempt.GT_EQ(sqlite.Int(MaxAttempts))).
		THEN(sqlite.String(string(StatusFailed))).
		ELSE(sqlite.String(string(StatusPending)))
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.NextAttemptAt, s.entries.LastError).
		SET(nextStatus, sqlite.String(sqliteent.SQLiteTime(retryAt)), sqlite.String(truncateCause(cause))).
		WHERE(s.claimedRow(tc, e))
	return s.settle(ctx, conn, tc, e, stmt)
}

// SECURITY/correctness: the attempt fences the settle, so a dispatcher whose lease expired cannot overwrite the lease of whoever re-claimed the entry.
func (s *sqliteStore) claimedRow(tc tenant.Context, e Entry) sqlite.BoolExpression {
	return s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
		AND(s.entries.ID.EQ(sqlite.String(e.ID.String()))).
		AND(s.entries.Status.EQ(sqlite.String(string(StatusPending)))).
		AND(s.entries.Attempt.EQ(sqlite.Int(int64(e.Attempt))))
}

type sqliteExecutable interface {
	ExecContext(ctx context.Context, db qrm.Executable) (sql.Result, error)
}

func (s *sqliteStore) settle(ctx context.Context, conn sqliteConn, tc tenant.Context, e Entry, stmt sqliteExecutable) error {
	res, err := stmt.ExecContext(ctx, conn)
	if err != nil {
		return fmt.Errorf("outbox.sqlite: settle: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("outbox.sqlite: settle: rows affected: %w", raErr)
	}
	if n > 0 {
		return nil
	}
	return s.refuseReason(ctx, conn, tc, e)
}

func (s *sqliteStore) refuseReason(ctx context.Context, conn sqliteConn, tc tenant.Context, e Entry) error {
	stmt := sqlite.SELECT(s.entries.Status, s.entries.Attempt).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.ID.EQ(sqlite.String(e.ID.String()))))
	var row struct {
		Status  string `alias:"outbox_entries.status"`
		Attempt int32  `alias:"outbox_entries.attempt"`
	}
	if err := stmt.QueryContext(ctx, conn, &row); err != nil {
		if errorIsNoRows(err) {
			return &NotFoundError{ID: e.ID.String()}
		}
		return fmt.Errorf("outbox.sqlite: settle: read status: %w", err)
	}
	if Status(row.Status) == StatusPending {
		return &StaleClaimError{ID: e.ID.String(), Claimed: e.Attempt, Current: int(row.Attempt)}
	}
	return &TerminalStateError{ID: e.ID.String(), Status: Status(row.Status)}
}

func (s *sqliteStore) Requeue(ctx context.Context, id uuid.UUID, target string) error {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return err
	}
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.Attempt, s.entries.NextAttemptAt, s.entries.LastError).
		SET(sqlite.String(string(StatusPending)), sqlite.Int(0), sqlite.String(sqliteent.SQLiteTime(time.Now())), sqlite.String("")).
		WHERE(s.failedRow(tc, id, target))
	res, execErr := stmt.ExecContext(ctx, conn)
	if execErr != nil {
		return fmt.Errorf("outbox.sqlite.Requeue: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("outbox.sqlite.Requeue: rows affected: %w", raErr)
	}
	if n > 0 {
		return nil
	}
	return s.refuseRequeue(ctx, conn, tc, id, target)
}

func (s *sqliteStore) failedRow(tc tenant.Context, id uuid.UUID, target string) sqlite.BoolExpression {
	return s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
		AND(s.entries.ID.EQ(sqlite.String(id.String()))).
		AND(s.entries.Target.EQ(sqlite.String(target))).
		AND(s.entries.Status.EQ(sqlite.String(string(StatusFailed))))
}

func (s *sqliteStore) refuseRequeue(ctx context.Context, conn sqliteConn, tc tenant.Context, id uuid.UUID, target string) error {
	stmt := sqlite.SELECT(s.entries.Status).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.ID.EQ(sqlite.String(id.String()))).
			AND(s.entries.Target.EQ(sqlite.String(target))))
	var row struct {
		Status string `alias:"outbox_entries.status"`
	}
	if err := stmt.QueryContext(ctx, conn, &row); err != nil {
		if errorIsNoRows(err) {
			return &NotFoundError{ID: id.String()}
		}
		return fmt.Errorf("outbox.sqlite.Requeue: read status: %w", err)
	}
	return &NotFailedError{ID: id.String(), Status: Status(row.Status)}
}

func (s *sqliteStore) RequeueFailed(ctx context.Context, target string) (int, error) {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return 0, err
	}
	stmt := s.entries.UPDATE(s.entries.Status, s.entries.Attempt, s.entries.NextAttemptAt, s.entries.LastError).
		SET(sqlite.String(string(StatusPending)), sqlite.Int(0), sqlite.String(sqliteent.SQLiteTime(time.Now())), sqlite.String("")).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.Target.EQ(sqlite.String(target))).
			AND(s.entries.Status.EQ(sqlite.String(string(StatusFailed)))))
	res, execErr := stmt.ExecContext(ctx, conn)
	if execErr != nil {
		return 0, fmt.Errorf("outbox.sqlite.RequeueFailed: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return 0, fmt.Errorf("outbox.sqlite.RequeueFailed: rows affected: %w", raErr)
	}
	return int(n), nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID, target string) (Entry, error) {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return Entry{}, err
	}
	stmt := sqlite.SELECT(s.entries.AllColumns).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.ID.EQ(sqlite.String(id.String()))).
			AND(s.entries.Target.EQ(sqlite.String(target))))

	var row sqliteEntryRow
	if qErr := stmt.QueryContext(ctx, conn, &row); qErr != nil {
		if errorIsNoRows(qErr) {
			return Entry{}, &NotFoundError{ID: id.String()}
		}
		return Entry{}, fmt.Errorf("outbox.sqlite.ByID: %w", qErr)
	}
	return row.toEntry()
}

func (s *sqliteStore) ListByTarget(ctx context.Context, target string, limit int) ([]Entry, error) {
	conn, tc, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}
	stmt := sqlite.SELECT(s.entries.AllColumns).
		FROM(s.entries).
		WHERE(s.entries.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.entries.Target.EQ(sqlite.String(target)))).
		ORDER_BY(s.entries.CreatedAt.DESC(), s.entries.ID.DESC()).
		LIMIT(listBatch(limit))

	var rows []sqliteEntryRow
	if qErr := stmt.QueryContext(ctx, conn, &rows); qErr != nil && !errorIsNoRows(qErr) {
		return nil, fmt.Errorf("outbox.sqlite.ListByTarget: %w", qErr)
	}
	out := make([]Entry, 0, len(rows))
	for i := range rows {
		e, cErr := rows[i].toEntry()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, e)
	}
	return out, nil
}
