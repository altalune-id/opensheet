package credential

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

func (s *postgresStore) Save(ctx context.Context, c *Credential) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			c.ID, c.OrgID, c.ProjectID, c.Name, string(c.Kind), string(c.Status),
			c.AuthorizedByUserID, c.GoogleAccountEmail, c.Sealed,
			c.CreatedAt.UTC(), c.UpdatedAt.UTC(),
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			postgres.SET(
				s.table.Name.SET(postgres.String(c.Name)),
				s.table.Kind.SET(postgres.String(string(c.Kind))),
				s.table.Status.SET(postgres.String(string(c.Status))),
				s.table.GoogleAccountEmail.SET(postgres.String(c.GoogleAccountEmail)),
				s.table.Sealed.SET(postgres.Bytea(c.Sealed)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(c.UpdatedAt.UTC())),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		if pgSQLState(execErr) == sqlStateUniqueViolation {
			return s.endTx(tx, owned, &AlreadyExistsError{Name: c.Name})
		}
		return s.endTx(tx, owned, fmt.Errorf("credential.postgres.Save: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().WHERE(s.table.ID.EQ(postgres.UUID(id)))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		if pgSQLState(execErr) == sqlStateForeignKeyViolation {
			return s.endTx(tx, owned, &InUseError{ID: id.String()})
		}
		return s.endTx(tx, owned, fmt.Errorf("credential.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("credential.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}

func pgSQLState(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	return pgErr.Code
}
