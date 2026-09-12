package spreadsheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
)

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Spreadsheet, error) {
	return s.queryOne(ctx,
		s.table.ID.EQ(postgres.UUID(id)),
		&NotFoundError{ID: id.String()},
		"ByID",
	)
}

func (s *postgresStore) ByGoogleFileID(ctx context.Context, orgID, projectID uuid.UUID, fileID string) (*Spreadsheet, error) {
	return s.queryOne(ctx,
		s.table.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.table.ProjectID.EQ(postgres.UUID(projectID))).
			AND(s.table.GoogleFileID.EQ(postgres.String(fileID))),
		&NotFoundError{ProjectID: projectID.String(), GoogleFileID: fileID},
		"ByGoogleFileID",
	)
}

func (s *postgresStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Spreadsheet, error) {
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
		ORDER_BY(s.table.CreatedAt.ASC(), s.table.ID.ASC())
	var rows []pgSpreadsheetRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("spreadsheet.postgres.List: %w", qErr)
	}
	out := make([]*Spreadsheet, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toSpreadsheet())
	}
	return out, nil
}

func (s *postgresStore) queryOne(ctx context.Context, cond postgres.BoolExpression, notFound *NotFoundError, op string) (*Spreadsheet, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row pgSpreadsheetRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("spreadsheet.postgres.%s: %w", op, qErr)
	}
	return row.toSpreadsheet(), nil
}
