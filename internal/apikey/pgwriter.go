package apikey

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
)

func (s *postgresStore) Save(ctx context.Context, k *APIKey) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveTx(ctx, tx, k))
}

func (s *postgresStore) saveTx(ctx context.Context, tx *sql.Tx, k *APIKey) error {
	if err := s.writeKey(ctx, tx, k); err != nil {
		return err
	}
	return s.writeGrant(ctx, tx, k)
}

// SECURITY: a key read through PublicColumns carries no hash, so that case updates in place instead of
// upserting a NULL secret_hash over a live key.
func (s *postgresStore) writeKey(ctx context.Context, tx *sql.Tx, k *APIKey) error {
	if len(k.SecretHash) == 0 {
		return s.updateKey(ctx, tx, k)
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			k.ID, k.OrgID, k.ProjectID, k.Name, k.KeyPrefix, k.SecretHash,
			scopeArray(k.Scopes),
			pgNullableTime(k.ExpiresAt), pgNullableTime(k.LastUsedAt), pgNullableTime(k.RevokedAt),
			k.CreatedAt.UTC(),
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(postgres.SET(s.mutableAssignments(k)...))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		if isPostgresUniqueViolation(err) {
			return &AlreadyExistsError{Field: "key_prefix", Value: k.KeyPrefix}
		}
		return fmt.Errorf("apikey.postgres.Save: %w", err)
	}
	return nil
}

func (s *postgresStore) updateKey(ctx context.Context, tx *sql.Tx, k *APIKey) error {
	stmt := s.table.
		UPDATE(s.table.Name, s.table.Scopes, s.table.ExpiresAt, s.table.LastUsedAt, s.table.RevokedAt).
		SET(
			postgres.String(k.Name),
			scopesExpr(k.Scopes),
			pgNullableTimeExpr(k.ExpiresAt),
			pgNullableTimeExpr(k.LastUsedAt),
			pgNullableTimeExpr(k.RevokedAt),
		).
		WHERE(s.table.ID.EQ(postgres.UUID(k.ID)))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("apikey.postgres.Save: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apikey.postgres.Save: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: k.ID.String()}
	}
	return nil
}

func (s *postgresStore) writeGrant(ctx context.Context, tx *sql.Tx, k *APIKey) error {
	del := s.grants.DELETE().WHERE(s.grants.APIKeyID.EQ(postgres.UUID(k.ID)))
	if _, err := del.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.postgres.Save: clear grant: %w", err)
	}
	if len(k.SheetIDs) == 0 {
		return nil
	}
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range k.SheetIDs {
		ins = ins.VALUES(k.ID, id, k.OrgID)
	}
	if _, err := ins.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.postgres.Save: write grant: %w", err)
	}
	return nil
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().WHERE(s.table.ID.EQ(postgres.UUID(id)))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("apikey.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("apikey.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) mutableAssignments(k *APIKey) []postgres.ColumnAssigment {
	return []postgres.ColumnAssigment{
		s.table.Name.SET(postgres.String(k.Name)),
		s.table.Scopes.SET(scopesExpr(k.Scopes)),
		s.table.ExpiresAt.SET(pgNullableTimeExpr(k.ExpiresAt)),
		s.table.LastUsedAt.SET(pgNullableTimeExpr(k.LastUsedAt)),
		s.table.RevokedAt.SET(pgNullableTimeExpr(k.RevokedAt)),
	}
}

// NOTE: go-jet v2 has no array column type, so the text[] value goes in as a bound pgx []string behind a cast.
func scopesExpr(scopes []string) postgres.StringExpression {
	return postgres.RawString("#scopes::text[]", postgres.RawArgs{"#scopes": scopeArray(scopes)})
}
