package credential

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
)

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Credential, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.ID.EQ(postgres.UUID(id))).
		LIMIT(1)
	var row pgCredentialRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("credential.postgres.ByID: %w", qErr)
	}
	return row.toCredential(), nil
}

func (s *postgresStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Credential, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.table.ProjectID.EQ(postgres.UUID(projectID)))).
		ORDER_BY(s.table.CreatedAt.DESC())
	var rows []pgCredentialRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("credential.postgres.List: %w", qErr)
	}
	out := make([]*Credential, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toCredential())
	}
	return out, nil
}
