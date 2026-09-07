package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/template/internal/platform/db"
	pgent "altalune.id/template/internal/platform/db/entity/postgres"
	"altalune.id/template/internal/platform/tenant"
)

const pgUniqueViolation = "23505"

type postgresStore struct {
	pool  pdb.Pool
	pc    *tenant.PgConn
	table *pgent.Projects
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, table: pgent.NewProjects(schema, tablePrefix)}
}

type pgProjectRow struct {
	ID        uuid.UUID `alias:"projects.id"`
	OrgID     uuid.UUID `alias:"projects.org_id"`
	Slug      string    `alias:"projects.slug"`
	Name      string    `alias:"projects.name"`
	CreatedAt time.Time `alias:"projects.created_at"`
	System    bool      `alias:"projects.system"`
}

func (r *pgProjectRow) toProject() *Project {
	return &Project{
		ID:        r.ID,
		OrgID:     r.OrgID,
		Slug:      r.Slug,
		Name:      r.Name,
		CreatedAt: r.CreatedAt,
		System:    r.System,
	}
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		tc, _ := tenant.From(ctx)
		return tx, false, tc, nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("project.postgres: begin: %w", err)
	}
	return tx, true, tc, nil
}

func (s *postgresStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("project.postgres: commit: %w", cerr)
	}
	return nil
}

func isPgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgUniqueViolation
	}
	return false
}
