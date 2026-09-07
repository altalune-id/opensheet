package user

import (
	"context"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
)

func (s *postgresStore) Save(ctx context.Context, u *User) error {
	isAdmin := u.IsAdmin || u.Source == SourceGenesis || u.Source == SourceLocal
	now := time.Now().UTC()
	stmt := s.table.INSERT(
		s.table.ID,
		s.table.Email,
		s.table.Name,
		s.table.AvatarURL,
		s.table.PasswordHash,
		s.table.IsAdmin,
		s.table.Locale,
		s.table.TermsAcceptedAt,
		s.table.CreatedAt,
		s.table.UpdatedAt,
	).
		VALUES(
			u.ID,
			u.Email,
			u.Name,
			"",
			u.PasswordHash,
			isAdmin,
			u.Locale,
			nullableTimeExpr(u.TermsAcceptedAt),
			u.CreatedAt.UTC(),
			now,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(postgres.SET(
			s.table.Email.SET(postgres.String(u.Email)),
			s.table.Name.SET(postgres.String(u.Name)),
			s.table.IsAdmin.SET(postgres.Bool(isAdmin)),
			s.table.PasswordHash.SET(postgres.String(u.PasswordHash)),
			s.table.Locale.SET(postgres.String(u.Locale)),
			s.table.TermsAcceptedAt.SET(nullableTimeExpr(u.TermsAcceptedAt)),
			s.table.UpdatedAt.SET(postgres.TimestampzT(now)),
		))
	if _, err := stmt.ExecContext(ctx, s.writer(ctx)); err != nil {
		if isPGUniqueViolation(err) {
			return &AlreadyExistsError{Field: "email", Value: u.Email}
		}
		return fmt.Errorf("user.postgres.Save: %w", err)
	}
	return nil
}

func (s *postgresStore) UpdateLocale(ctx context.Context, id uuid.UUID, locale string) error {
	stmt := s.table.UPDATE(s.table.Locale, s.table.UpdatedAt).
		SET(postgres.String(locale), postgres.TimestampzT(time.Now().UTC())).
		WHERE(s.table.ID.EQ(postgres.UUID(id)))
	res, err := stmt.ExecContext(ctx, s.writer(ctx))
	if err != nil {
		return fmt.Errorf("user.postgres.UpdateLocale: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr == nil && n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func nullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return postgres.TimestampzExp(postgres.NULL)
	}
	return postgres.TimestampzT(t.UTC())
}
