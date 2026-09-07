package onboard

import (
	"context"
	"fmt"
)

func (s *postgresStore) Save(ctx context.Context, b *Bootstrap) error {
	stmt := s.table.INSERT(s.table.ID, s.table.OnboardedAt, s.table.OnboardedBy, s.table.Method).
		VALUES(int64(1), b.OnboardedAt.UTC(), b.OnboardedBy, string(b.Method)).
		ON_CONFLICT(s.table.ID).
		DO_NOTHING()
	res, err := stmt.ExecContext(ctx, s.writer(ctx))
	if err != nil {
		if isPGUniqueViolation(err) {
			return &AlreadyOnboardedError{}
		}
		return fmt.Errorf("onboard.postgres.Save: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("onboard.postgres.Save: rows affected: %w", raErr)
	}
	if n == 0 {
		return &AlreadyOnboardedError{}
	}
	return nil
}
