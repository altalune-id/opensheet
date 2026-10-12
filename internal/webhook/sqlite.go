package webhook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db        *sql.DB
	endpoints *sqliteent.WebhookEndpoints
	attempts  *sqliteent.WebhookDeliveries
}

func newSQLiteStore(sqlDB *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:        sqlDB,
		endpoints: sqliteent.NewWebhookEndpoints(tablePrefix),
		attempts:  sqliteent.NewWebhookDeliveries(tablePrefix),
	}
}

type sqliteEndpointRow struct {
	ID              string `alias:"webhook_endpoints.id"`
	OrgID           string `alias:"webhook_endpoints.org_id"`
	ProjectID       string `alias:"webhook_endpoints.project_id"`
	URL             string `alias:"webhook_endpoints.url"`
	Description     string `alias:"webhook_endpoints.description"`
	EventTypes      string `alias:"webhook_endpoints.event_types"`
	SecretPrimary   []byte `alias:"webhook_endpoints.secret_primary"`
	SecretSecondary []byte `alias:"webhook_endpoints.secret_secondary"`
	Active          int64  `alias:"webhook_endpoints.active"`
	CreatedAt       string `alias:"webhook_endpoints.created_at"`
	UpdatedAt       string `alias:"webhook_endpoints.updated_at"`
}

func (r *sqliteEndpointRow) toEndpoint() (*Endpoint, error) {
	ids, err := parseUUIDs(r.ID, r.OrgID, r.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("webhook.sqlite: endpoint %w", err)
	}
	types, err := unmarshalEventTypes(r.EventTypes)
	if err != nil {
		return nil, fmt.Errorf("webhook.sqlite: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("webhook.sqlite: parse created_at: %w", err)
	}
	ua, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("webhook.sqlite: parse updated_at: %w", err)
	}
	return &Endpoint{
		ID:          ids[0],
		OrgID:       ids[1],
		ProjectID:   ids[2],
		URL:         r.URL,
		Description: r.Description,
		EventTypes:  types,
		Secrets:     SealedSecrets{Primary: r.SecretPrimary, Secondary: r.SecretSecondary},
		Active:      r.Active != 0,
		CreatedAt:   ca.UTC(),
		UpdatedAt:   ua.UTC(),
	}, nil
}

type sqliteAttemptRow struct {
	ID                string `alias:"webhook_deliveries.id"`
	OrgID             string `alias:"webhook_deliveries.org_id"`
	ProjectID         string `alias:"webhook_deliveries.project_id"`
	EndpointID        string `alias:"webhook_deliveries.endpoint_id"`
	DeliveryID        string `alias:"webhook_deliveries.delivery_id"`
	EventID           string `alias:"webhook_deliveries.event_id"`
	EventType         string `alias:"webhook_deliveries.event_type"`
	Attempt           int    `alias:"webhook_deliveries.attempt"`
	StatusCode        int    `alias:"webhook_deliveries.status_code"`
	Error             string `alias:"webhook_deliveries.error"`
	ResponseBody      string `alias:"webhook_deliveries.response_body"`
	ResponseTruncated int64  `alias:"webhook_deliveries.response_truncated"`
	ResponseHeaders   string `alias:"webhook_deliveries.response_headers"`
	DurationMs        int64  `alias:"webhook_deliveries.duration_ms"`
	CreatedAt         string `alias:"webhook_deliveries.created_at"`
}

func (r *sqliteAttemptRow) toAttempt() (Attempt, error) {
	ids, err := parseUUIDs(r.ID, r.OrgID, r.ProjectID, r.EndpointID, r.DeliveryID, r.EventID)
	if err != nil {
		return Attempt{}, fmt.Errorf("webhook.sqlite: attempt %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return Attempt{}, fmt.Errorf("webhook.sqlite: parse attempt created_at: %w", err)
	}
	headers, err := unmarshalHeaders(r.ResponseHeaders)
	if err != nil {
		return Attempt{}, fmt.Errorf("webhook.sqlite: attempt %w", err)
	}
	return Attempt{
		ID:                ids[0],
		OrgID:             ids[1],
		ProjectID:         ids[2],
		EndpointID:        ids[3],
		DeliveryID:        ids[4],
		EventID:           ids[5],
		EventType:         events.Type(r.EventType),
		Attempt:           r.Attempt,
		StatusCode:        r.StatusCode,
		Error:             r.Error,
		ResponseBody:      r.ResponseBody,
		ResponseTruncated: r.ResponseTruncated != 0,
		ResponseHeaders:   headers,
		Duration:          time.Duration(r.DurationMs) * time.Millisecond,
		CreatedAt:         ca.UTC(),
	}, nil
}

func parseUUIDs(raw ...string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, len(raw))
	for i, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("parse id %q: %w", s, err)
		}
		out[i] = id
	}
	return out, nil
}

// NOTE: enrolls in the caller's unit of work, so a write never opens a second SQLite writer transaction.
func (s *sqliteStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, tc, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("webhook.sqlite: begin: %w", err)
	}
	return tx, true, tc, nil
}

func (s *sqliteStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("webhook.sqlite: commit: %w", cerr)
	}
	return nil
}

func (s *sqliteStore) Save(ctx context.Context, e *Endpoint) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, e))
}

func (s *sqliteStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, e *Endpoint) error {
	if e.OrgID != tc.OrgID {
		// SECURITY: SQLite has no RLS, so a fresh row naming another org would otherwise land in it.
		return &NotFoundError{ID: e.ID.String()}
	}
	types, err := marshalEventTypes(e.EventTypes)
	if err != nil {
		return err
	}
	updatedAt := sqliteent.SQLiteTime(e.UpdatedAt)
	stmt := s.endpoints.INSERT(s.endpoints.AllColumns).
		VALUES(
			e.ID.String(),
			e.OrgID.String(),
			e.ProjectID.String(),
			e.URL,
			e.Description,
			types,
			sqlite.Blob(e.Secrets.Primary),
			sqliteSecondary(e.Secrets.Secondary),
			sqliteBool(e.Active),
			sqliteent.SQLiteTime(e.CreatedAt),
			updatedAt,
		).
		ON_CONFLICT(s.endpoints.ID).
		// SECURITY: the conflict clause is guarded by org; SQLite has no RLS behind this.
		DO_UPDATE(
			sqlite.SET(
				s.endpoints.URL.SET(sqlite.String(e.URL)),
				s.endpoints.Description.SET(sqlite.String(e.Description)),
				s.endpoints.EventTypes.SET(sqlite.String(types)),
				s.endpoints.Active.SET(sqlite.Int(sqliteBool(e.Active))),
				s.endpoints.UpdatedAt.SET(sqlite.String(updatedAt)),
			).WHERE(s.endpoints.OrgID.EQ(sqlite.String(tc.OrgID.String()))),
		)
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("webhook.sqlite.Save: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("webhook.sqlite.Save: rows affected: %w", err)
	}
	// NOTE: zero rows means the conflict clause's org guard refused.
	if n == 0 {
		return &NotFoundError{ID: e.ID.String()}
	}
	return nil
}

func (s *sqliteStore) SaveSecrets(ctx context.Context, id uuid.UUID, expected, next SealedSecrets) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveSecrets(ctx, tx, tc, id, expected, next))
}

func (s *sqliteStore) saveSecrets(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, expected, next SealedSecrets) error {
	stmt := s.endpoints.UPDATE().
		SET(
			s.endpoints.SecretPrimary.SET(sqlite.Blob(next.Primary)),
			s.endpoints.SecretSecondary.SET(sqliteSecondary(next.Secondary)),
			s.endpoints.UpdatedAt.SET(sqlite.String(sqliteent.SQLiteTime(time.Now()))),
		).
		WHERE(s.endpoints.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.endpoints.ID.EQ(sqlite.String(id.String()))).
			AND(s.endpoints.SecretPrimary.EQ(sqlite.Blob(expected.Primary))).
			AND(s.endpoints.SecretSecondary.IS_NOT_DISTINCT_FROM(sqliteSecondary(expected.Secondary))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("webhook.sqlite.SaveSecrets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("webhook.sqlite.SaveSecrets: rows affected: %w", err)
	}
	if n > 0 {
		return nil
	}
	probe := sqlite.SELECT(s.endpoints.ID).
		FROM(s.endpoints).
		WHERE(s.endpoints.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.endpoints.ID.EQ(sqlite.String(id.String()))))
	var row struct {
		ID string `alias:"webhook_endpoints.id"`
	}
	if err := probe.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return &NotFoundError{ID: id.String()}
		}
		return fmt.Errorf("webhook.sqlite.SaveSecrets: probe: %w", err)
	}
	return &SecretConflictError{ID: id.String()}
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Endpoint, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := sqlite.SELECT(s.endpoints.AllColumns).
		FROM(s.endpoints).
		WHERE(s.endpoints.ID.EQ(sqlite.String(id.String())).
			AND(s.endpoints.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		LIMIT(1)
	var row sqliteEndpointRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("webhook.sqlite.ByID: %w", err)
	}
	return row.toEndpoint()
}

func (s *sqliteStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Endpoint, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := sqlite.SELECT(s.endpoints.AllColumns).
		FROM(s.endpoints).
		WHERE(s.endpoints.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.endpoints.OrgID.EQ(sqlite.String(tc.OrgID.String()))).
			AND(s.endpoints.ProjectID.EQ(sqlite.String(projectID.String())))).
		ORDER_BY(s.endpoints.CreatedAt.DESC(), s.endpoints.ID.DESC())
	var rows []sqliteEndpointRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil {
		return nil, fmt.Errorf("webhook.sqlite.List: %w", err)
	}
	out := make([]*Endpoint, 0, len(rows))
	for i := range rows {
		e, err := rows[i].toEndpoint()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *sqliteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.delete(ctx, tx, tc, id))
}

func (s *sqliteStore) delete(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID) error {
	stmt := s.endpoints.DELETE().
		WHERE(s.endpoints.ID.EQ(sqlite.String(id.String())).
			AND(s.endpoints.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("webhook.sqlite.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("webhook.sqlite.Delete: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func (s *sqliteStore) SaveAttempt(ctx context.Context, a Attempt) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveAttempt(ctx, tx, tc, a))
}

func (s *sqliteStore) saveAttempt(ctx context.Context, tx *sql.Tx, tc tenant.Context, a Attempt) error {
	a, err := prepareAttempt(tc, a)
	if err != nil {
		return err
	}
	headers, err := marshalHeaders(a.ResponseHeaders)
	if err != nil {
		return err
	}
	stmt := s.attempts.INSERT(s.attempts.AllColumns).
		VALUES(
			a.ID.String(),
			a.OrgID.String(),
			a.ProjectID.String(),
			a.EndpointID.String(),
			a.DeliveryID.String(),
			a.EventID.String(),
			string(a.EventType),
			a.Attempt,
			a.StatusCode,
			a.Error,
			a.ResponseBody,
			sqliteBool(a.ResponseTruncated),
			headers,
			a.Duration.Milliseconds(),
			sqliteent.SQLiteTime(a.CreatedAt),
		)
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		if isSQLiteForeignKeyViolation(err) {
			return &NotFoundError{ID: a.EndpointID.String()}
		}
		return fmt.Errorf("webhook.sqlite.SaveAttempt: %w", err)
	}
	return nil
}

func (s *sqliteStore) ListAttempts(ctx context.Context, endpointID, deliveryID uuid.UUID) ([]Attempt, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := sqlite.SELECT(s.attempts.AllColumns).
		FROM(s.attempts).
		WHERE(s.attempts.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.attempts.EndpointID.EQ(sqlite.String(endpointID.String()))).
			AND(s.attempts.DeliveryID.EQ(sqlite.String(deliveryID.String())))).
		ORDER_BY(s.attempts.CreatedAt.DESC(), s.attempts.ID.DESC())
	var rows []sqliteAttemptRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil {
		return nil, fmt.Errorf("webhook.sqlite.ListAttempts: %w", err)
	}
	out := make([]Attempt, 0, len(rows))
	for i := range rows {
		a, err := rows[i].toAttempt()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func sqliteSecondary(b []byte) sqlite.BlobExpression {
	if len(b) == 0 {
		return sqliteent.NullBlob()
	}
	return sqlite.Blob(b)
}

func sqliteBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func isSQLiteForeignKeyViolation(err error) bool {
	sqliteErr, ok := errors.AsType[*sqlitedrv.Error](err)
	return ok && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}
