package webhook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresStore struct {
	pool      pdb.Pool
	pc        *tenant.PgConn
	endpoints *pgent.WebhookEndpoints
	attempts  *pgent.WebhookDeliveries
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{
		pool:      pool,
		pc:        pc,
		endpoints: pgent.NewWebhookEndpoints(schema, tablePrefix),
		attempts:  pgent.NewWebhookDeliveries(schema, tablePrefix),
	}
}

type pgEndpointRow struct {
	ID              uuid.UUID `alias:"webhook_endpoints.id"`
	OrgID           uuid.UUID `alias:"webhook_endpoints.org_id"`
	ProjectID       uuid.UUID `alias:"webhook_endpoints.project_id"`
	URL             string    `alias:"webhook_endpoints.url"`
	Description     string    `alias:"webhook_endpoints.description"`
	EventTypes      string    `alias:"webhook_endpoints.event_types"`
	SecretPrimary   []byte    `alias:"webhook_endpoints.secret_primary"`
	SecretSecondary []byte    `alias:"webhook_endpoints.secret_secondary"`
	Active          bool      `alias:"webhook_endpoints.active"`
	CreatedAt       time.Time `alias:"webhook_endpoints.created_at"`
	UpdatedAt       time.Time `alias:"webhook_endpoints.updated_at"`
}

func (r *pgEndpointRow) toEndpoint() (*Endpoint, error) {
	types, err := unmarshalEventTypes(r.EventTypes)
	if err != nil {
		return nil, fmt.Errorf("webhook.postgres: %w", err)
	}
	return &Endpoint{
		ID:          r.ID,
		OrgID:       r.OrgID,
		ProjectID:   r.ProjectID,
		URL:         r.URL,
		Description: r.Description,
		EventTypes:  types,
		Secrets:     SealedSecrets{Primary: r.SecretPrimary, Secondary: r.SecretSecondary},
		Active:      r.Active,
		CreatedAt:   r.CreatedAt.UTC(),
		UpdatedAt:   r.UpdatedAt.UTC(),
	}, nil
}

type pgAttemptRow struct {
	ID                uuid.UUID `alias:"webhook_deliveries.id"`
	OrgID             uuid.UUID `alias:"webhook_deliveries.org_id"`
	ProjectID         uuid.UUID `alias:"webhook_deliveries.project_id"`
	EndpointID        uuid.UUID `alias:"webhook_deliveries.endpoint_id"`
	DeliveryID        uuid.UUID `alias:"webhook_deliveries.delivery_id"`
	EventID           uuid.UUID `alias:"webhook_deliveries.event_id"`
	EventType         string    `alias:"webhook_deliveries.event_type"`
	Attempt           int       `alias:"webhook_deliveries.attempt"`
	StatusCode        int       `alias:"webhook_deliveries.status_code"`
	Error             string    `alias:"webhook_deliveries.error"`
	ResponseBody      string    `alias:"webhook_deliveries.response_body"`
	ResponseTruncated bool      `alias:"webhook_deliveries.response_truncated"`
	ResponseHeaders   string    `alias:"webhook_deliveries.response_headers"`
	DurationMs        int64     `alias:"webhook_deliveries.duration_ms"`
	CreatedAt         time.Time `alias:"webhook_deliveries.created_at"`
}

func (r *pgAttemptRow) toAttempt() (Attempt, error) {
	headers, err := unmarshalHeaders(r.ResponseHeaders)
	if err != nil {
		return Attempt{}, fmt.Errorf("webhook.postgres: attempt %w", err)
	}
	return Attempt{
		ID:                r.ID,
		OrgID:             r.OrgID,
		ProjectID:         r.ProjectID,
		EndpointID:        r.EndpointID,
		DeliveryID:        r.DeliveryID,
		EventID:           r.EventID,
		EventType:         events.Type(r.EventType),
		Attempt:           r.Attempt,
		StatusCode:        r.StatusCode,
		Error:             r.Error,
		ResponseBody:      r.ResponseBody,
		ResponseTruncated: r.ResponseTruncated,
		ResponseHeaders:   headers,
		Duration:          time.Duration(r.DurationMs) * time.Millisecond,
		CreatedAt:         r.CreatedAt.UTC(),
	}, nil
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
		return nil, false, tenant.Context{}, fmt.Errorf("webhook.postgres: begin: %w", err)
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
		return fmt.Errorf("webhook.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) Save(ctx context.Context, e *Endpoint) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, e))
}

func (s *postgresStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, e *Endpoint) error {
	if e.OrgID != tc.OrgID {
		// SECURITY: without RLS in force, a fresh row naming another org would otherwise land in it.
		return &NotFoundError{ID: e.ID.String()}
	}
	types, err := marshalEventTypes(e.EventTypes)
	if err != nil {
		return err
	}
	stmt := s.endpoints.INSERT(s.endpoints.AllColumns).
		VALUES(
			e.ID, e.OrgID, e.ProjectID,
			e.URL, e.Description, types,
			postgres.Bytea(e.Secrets.Primary),
			pgSecondary(e.Secrets.Secondary),
			e.Active,
			e.CreatedAt.UTC(), e.UpdatedAt.UTC(),
		).
		ON_CONFLICT(s.endpoints.ID).
		// SECURITY: the conflict clause is guarded by org, or an upsert carrying another
		// tenant's row id would rewrite that row wherever RLS is inert.
		DO_UPDATE(
			postgres.SET(
				s.endpoints.URL.SET(postgres.String(e.URL)),
				s.endpoints.Description.SET(postgres.String(e.Description)),
				s.endpoints.EventTypes.SET(postgres.String(types)),
				s.endpoints.Active.SET(postgres.Bool(e.Active)),
				s.endpoints.UpdatedAt.SET(postgres.TimestampzT(e.UpdatedAt.UTC())),
			).WHERE(s.endpoints.OrgID.EQ(postgres.UUID(tc.OrgID))),
		)
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("webhook.postgres.Save: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("webhook.postgres.Save: rows affected: %w", err)
	}
	// NOTE: zero rows means the conflict clause's org guard refused.
	if n == 0 {
		return &NotFoundError{ID: e.ID.String()}
	}
	return nil
}

func (s *postgresStore) SaveSecrets(ctx context.Context, id uuid.UUID, expected, next SealedSecrets) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveSecrets(ctx, tx, tc, id, expected, next))
}

func (s *postgresStore) saveSecrets(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, expected, next SealedSecrets) error {
	stmt := s.endpoints.UPDATE().
		SET(
			s.endpoints.SecretPrimary.SET(postgres.Bytea(next.Primary)),
			s.endpoints.SecretSecondary.SET(pgSecondary(next.Secondary)),
			s.endpoints.UpdatedAt.SET(postgres.TimestampzT(time.Now().UTC())),
		).
		WHERE(s.endpoints.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.endpoints.ID.EQ(postgres.UUID(id))).
			AND(s.endpoints.SecretPrimary.EQ(postgres.Bytea(expected.Primary))).
			AND(s.endpoints.SecretSecondary.IS_NOT_DISTINCT_FROM(pgSecondary(expected.Secondary))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("webhook.postgres.SaveSecrets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("webhook.postgres.SaveSecrets: rows affected: %w", err)
	}
	if n > 0 {
		return nil
	}
	probe := postgres.SELECT(s.endpoints.ID).
		FROM(s.endpoints).
		WHERE(s.endpoints.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.endpoints.ID.EQ(postgres.UUID(id))))
	var row struct {
		ID uuid.UUID `alias:"webhook_endpoints.id"`
	}
	if err := probe.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return &NotFoundError{ID: id.String()}
		}
		return fmt.Errorf("webhook.postgres.SaveSecrets: probe: %w", err)
	}
	return &SecretConflictError{ID: id.String()}
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Endpoint, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.endpoints.AllColumns).
		FROM(s.endpoints).
		WHERE(s.endpoints.ID.EQ(postgres.UUID(id)).
			AND(s.endpoints.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1)
	var row pgEndpointRow
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("webhook.postgres.ByID: %w", err)
	}
	return row.toEndpoint()
}

func (s *postgresStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Endpoint, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.endpoints.AllColumns).
		FROM(s.endpoints).
		WHERE(s.endpoints.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.endpoints.OrgID.EQ(postgres.UUID(tc.OrgID))).
			AND(s.endpoints.ProjectID.EQ(postgres.UUID(projectID)))).
		ORDER_BY(s.endpoints.CreatedAt.DESC(), s.endpoints.ID.DESC())
	var rows []pgEndpointRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil {
		return nil, fmt.Errorf("webhook.postgres.List: %w", err)
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

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.delete(ctx, tx, tc, id))
}

func (s *postgresStore) delete(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID) error {
	stmt := s.endpoints.DELETE().
		WHERE(s.endpoints.ID.EQ(postgres.UUID(id)).
			AND(s.endpoints.OrgID.EQ(postgres.UUID(tc.OrgID))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("webhook.postgres.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("webhook.postgres.Delete: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func (s *postgresStore) SaveAttempt(ctx context.Context, a Attempt) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveAttempt(ctx, tx, tc, a))
}

func (s *postgresStore) saveAttempt(ctx context.Context, tx *sql.Tx, tc tenant.Context, a Attempt) error {
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
			a.ID, a.OrgID, a.ProjectID, a.EndpointID, a.DeliveryID, a.EventID,
			string(a.EventType), a.Attempt, a.StatusCode, a.Error,
			a.ResponseBody, a.ResponseTruncated, headers,
			a.Duration.Milliseconds(), a.CreatedAt,
		)
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		if isPgForeignKeyViolation(err) {
			return &NotFoundError{ID: a.EndpointID.String()}
		}
		return fmt.Errorf("webhook.postgres.SaveAttempt: %w", err)
	}
	return nil
}

func (s *postgresStore) ListAttempts(ctx context.Context, endpointID, deliveryID uuid.UUID) ([]Attempt, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.attempts.AllColumns).
		FROM(s.attempts).
		WHERE(s.attempts.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.attempts.EndpointID.EQ(postgres.UUID(endpointID))).
			AND(s.attempts.DeliveryID.EQ(postgres.UUID(deliveryID)))).
		ORDER_BY(s.attempts.CreatedAt.DESC(), s.attempts.ID.DESC())
	var rows []pgAttemptRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil {
		return nil, fmt.Errorf("webhook.postgres.ListAttempts: %w", err)
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

func pgSecondary(b []byte) postgres.ByteaExpression {
	if len(b) == 0 {
		return pgent.NullBytea()
	}
	return postgres.Bytea(b)
}

func isPgForeignKeyViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23503"
}
