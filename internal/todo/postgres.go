package todo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	pdb "altalune.id/template/internal/platform/db"
	pgent "altalune.id/template/internal/platform/db/entity/postgres"
	"altalune.id/template/internal/platform/tenant"
)

type postgresStore struct {
	pool  pdb.Pool
	pc    *tenant.PgConn
	table *pgent.Todos
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, table: pgent.NewTodos(schema, tablePrefix)}
}

type pgTodoRow struct {
	ID        uuid.UUID `alias:"todos.id"`
	OrgID     uuid.UUID `alias:"todos.org_id"`
	ProjectID uuid.UUID `alias:"todos.project_id"`
	Title     string    `alias:"todos.title"`
	Done      bool      `alias:"todos.done"`
	CreatedAt time.Time `alias:"todos.created_at"`
	UpdatedAt time.Time `alias:"todos.updated_at"`
}

func (r *pgTodoRow) toTodo() *Todo {
	return &Todo{
		ID:        r.ID,
		OrgID:     r.OrgID,
		ProjectID: r.ProjectID,
		Title:     r.Title,
		Done:      r.Done,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
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
		return nil, false, tenant.Context{}, fmt.Errorf("todo.postgres: begin: %w", err)
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
		return fmt.Errorf("todo.postgres: commit: %w", cerr)
	}
	return nil
}
