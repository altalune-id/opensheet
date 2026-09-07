package onboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
)

type pgBootstrapRow struct {
	OnboardedAt time.Time `alias:"bootstrap.onboarded_at"`
	OnboardedBy string    `alias:"bootstrap.onboarded_by"`
	Method      string    `alias:"bootstrap.method"`
}

func (s *postgresStore) Get(ctx context.Context) (*Bootstrap, error) {
	stmt := postgres.SELECT(s.table.OnboardedAt, s.table.OnboardedBy, s.table.Method).
		FROM(s.table).
		WHERE(s.table.ID.EQ(postgres.Int(1))).
		LIMIT(1)
	var row pgBootstrapRow
	if err := stmt.QueryContext(ctx, s.reader(ctx), &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &NotOnboardedError{}
		}
		return nil, fmt.Errorf("onboard.postgres.Get: %w", err)
	}
	by, err := uuid.Parse(row.OnboardedBy)
	if err != nil {
		return nil, fmt.Errorf("onboard.postgres.Get: parse onboarded_by: %w", err)
	}
	return &Bootstrap{OnboardedAt: row.OnboardedAt.UTC(), OnboardedBy: by, Method: Method(row.Method)}, nil
}
