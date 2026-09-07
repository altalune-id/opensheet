package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
)

func (s *postgresStore) userSelectCols() []postgres.Projection {
	return []postgres.Projection{
		s.table.ID, s.table.Email, s.table.Name, s.table.IsAdmin,
		s.table.IDPIssuer, s.table.PasswordHash, s.table.Locale,
		s.table.TermsAcceptedAt, s.table.CreatedAt,
	}
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return s.queryOne(ctx, s.table.ID.EQ(postgres.UUID(id)), &NotFoundError{ID: id.String()})
}

func (s *postgresStore) ByEmail(ctx context.Context, email string) (*User, error) {
	return s.queryOne(ctx, s.table.Email.EQ(postgres.String(strings.ToLower(email))), &NotFoundError{Email: email})
}

func (s *postgresStore) queryOne(ctx context.Context, cond postgres.BoolExpression, notFound error) (*User, error) {
	cols := s.userSelectCols()
	stmt := postgres.SELECT(cols[0], cols[1:]...).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row pgUserRow
	if err := stmt.QueryContext(ctx, s.reader(ctx), &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("user.postgres.queryOne: %w", err)
	}
	return row.toUser(), nil
}

func (s *postgresStore) HasLocalUsers(ctx context.Context) (bool, error) {
	stmt := postgres.SELECT(s.table.ID).
		FROM(s.table).
		WHERE(s.table.PasswordHash.NOT_EQ(postgres.String(""))).
		LIMIT(1)
	var row struct {
		ID uuid.UUID `alias:"users.id"`
	}
	err := stmt.QueryContext(ctx, s.reader(ctx), &row)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("user.postgres.HasLocalUsers: %w", err)
}
