package onboard

import (
	"context"
	"errors"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/template/internal/platform/db"
	pgent "altalune.id/template/internal/platform/db/entity/postgres"
)

type postgresStore struct {
	pool  pdb.Pool
	table *pgent.Bootstrap
}

func newPostgresStore(pool pdb.Pool, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, table: pgent.NewBootstrap(schema, tablePrefix)}
}

func (s *postgresStore) writer(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.pool.W
}

func (s *postgresStore) reader(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.pool.R
}

func isPGUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
