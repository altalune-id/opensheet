package sheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
)

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Sheet, error) {
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
	var row pgSheetRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("sheet.postgres.ByID: %w", qErr)
	}
	return row.toSheet(), nil
}

func (s *postgresStore) BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*Sheet, error) {
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
			AND(s.table.ProjectID.EQ(postgres.UUID(projectID))).
			AND(s.table.Slug.EQ(postgres.String(slug)))).
		LIMIT(1)
	var row pgSheetRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{OrgID: orgID.String(), ProjectID: projectID.String(), Slug: slug}
		}
		return nil, fmt.Errorf("sheet.postgres.BySlug: %w", qErr)
	}
	return row.toSheet(), nil
}

func (s *postgresStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Sheet, error) {
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
		ORDER_BY(s.table.Slug.ASC())
	var rows []pgSheetRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("sheet.postgres.List: %w", qErr)
	}
	out := make([]*Sheet, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toSheet())
	}
	return out, nil
}
