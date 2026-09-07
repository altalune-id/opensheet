package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
)

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Project, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(
		s.table.ID, s.table.OrgID, s.table.Slug, s.table.Name, s.table.CreatedAt, s.table.System,
	).
		FROM(s.table).
		WHERE(s.table.ID.EQ(postgres.UUID(id))).
		LIMIT(1)
	var row pgProjectRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("project.postgres.ByID: %w", qErr)
	}
	return row.toProject(), nil
}

func (s *postgresStore) BySlug(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(
		s.table.ID, s.table.OrgID, s.table.Slug, s.table.Name, s.table.CreatedAt, s.table.System,
	).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.table.Slug.EQ(postgres.String(slug)))).
		LIMIT(1)
	var row pgProjectRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{OrgID: orgID.String(), Slug: slug}
		}
		return nil, fmt.Errorf("project.postgres.BySlug: %w", qErr)
	}
	return row.toProject(), nil
}

func (s *postgresStore) List(ctx context.Context, orgID uuid.UUID) ([]*Project, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(
		s.table.ID, s.table.OrgID, s.table.Slug, s.table.Name, s.table.CreatedAt, s.table.System,
	).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(postgres.UUID(orgID))).
		ORDER_BY(s.table.CreatedAt.ASC())
	var rows []pgProjectRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("project.postgres.List: %w", qErr)
	}
	out := make([]*Project, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toProject())
	}
	return out, nil
}
