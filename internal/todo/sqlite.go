package todo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	sqliteent "altalune.id/template/internal/platform/db/entity/sqlite"
	"altalune.id/template/internal/platform/tenant"
)

type sqliteStore struct {
	db    *sql.DB
	table *sqliteent.Todos
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewTodos(tablePrefix)}
}

type sqliteTodoRow struct {
	ID        string `alias:"todos.id"`
	OrgID     string `alias:"todos.org_id"`
	ProjectID string `alias:"todos.project_id"`
	Title     string `alias:"todos.title"`
	Done      int64  `alias:"todos.done"`
	CreatedAt string `alias:"todos.created_at"`
	UpdatedAt string `alias:"todos.updated_at"`
}

func (r *sqliteTodoRow) toTodo() (*Todo, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("todo.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("todo.sqlite: parse org_id: %w", err)
	}
	pid, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("todo.sqlite: parse project_id: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("todo.sqlite: parse created_at: %w", err)
	}
	ua, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("todo.sqlite: parse updated_at: %w", err)
	}
	return &Todo{
		ID:        id,
		OrgID:     oid,
		ProjectID: pid,
		Title:     r.Title,
		Done:      r.Done == 1,
		CreatedAt: ca,
		UpdatedAt: ua,
	}, nil
}

func (s *sqliteStore) Save(ctx context.Context, t *Todo) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	done := int64(0)
	if t.Done {
		done = 1
	}
	updatedAt := t.UpdatedAt.UTC().Format(time.RFC3339Nano)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			t.ID.String(),
			t.OrgID.String(),
			t.ProjectID.String(),
			tc.UserID.String(),
			t.Title,
			done,
			t.CreatedAt.UTC().Format(time.RFC3339Nano),
			updatedAt,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			sqlite.SET(
				s.table.Title.SET(sqlite.String(t.Title)),
				s.table.Done.SET(sqlite.Int(done)),
				s.table.UpdatedAt.SET(sqlite.String(updatedAt)),
			),
		)
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("todo.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Todo, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		LIMIT(1)
	var row sqliteTodoRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("todo.sqlite.ByID: %w", err)
	}
	return row.toTodo()
}

func (s *sqliteStore) List(ctx context.Context, orgID, projectID uuid.UUID, opts ListOpts) ([]*Todo, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return []*Todo{}, nil
	}
	where := s.table.OrgID.EQ(sqlite.String(orgID.String())).
		AND(s.table.ProjectID.EQ(sqlite.String(projectID.String())))
	if opts.Done != nil {
		val := int64(0)
		if *opts.Done {
			val = 1
		}
		where = where.AND(s.table.Done.EQ(sqlite.Int(val)))
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(where).
		ORDER_BY(s.table.CreatedAt.DESC())
	var rows []sqliteTodoRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("todo.sqlite.List: %w", err)
	}
	out := make([]*Todo, 0, len(rows))
	for i := range rows {
		td, err := rows[i].toTodo()
		if err != nil {
			return nil, err
		}
		out = append(out, td)
	}
	return out, nil
}

func (s *sqliteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, s.db)
	if err != nil {
		return fmt.Errorf("todo.sqlite.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("todo.sqlite.Delete: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func (s *sqliteStore) ClearDone(ctx context.Context, orgID, projectID uuid.UUID) (int, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	if tc.OrgID != orgID {
		return 0, nil
	}
	stmt := s.table.DELETE().
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String()))).
			AND(s.table.Done.EQ(sqlite.Int(1))))
	res, err := stmt.ExecContext(ctx, s.db)
	if err != nil {
		return 0, fmt.Errorf("todo.sqlite.ClearDone: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("todo.sqlite.ClearDone: rows affected: %w", err)
	}
	return int(n), nil
}

// MarkDoneOlderThan marks stale open todos done in batches. NOTE: each batch commits separately; the sweep is idempotent.
func (s *sqliteStore) MarkDoneOlderThan(ctx context.Context, orgID uuid.UUID, cutoff time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = SweepBatchSize
	}
	cut := cutoff.UTC().Format(time.RFC3339Nano)
	total := 0
	for {
		stale := sqlite.SELECT(s.table.ID).
			FROM(s.table).
			WHERE(
				s.table.OrgID.EQ(sqlite.String(orgID.String())).
					AND(s.table.Done.EQ(sqlite.Int(0))).
					AND(s.table.CreatedAt.LT(sqlite.String(cut))),
			).
			ORDER_BY(s.table.CreatedAt.ASC()).
			LIMIT(int64(batch))

		stmt := s.table.UPDATE(s.table.Done, s.table.UpdatedAt).
			SET(sqlite.Int(1), sqlite.String(time.Now().UTC().Format(time.RFC3339Nano))).
			WHERE(s.table.ID.IN(stale))

		res, err := stmt.ExecContext(ctx, s.db)
		if err != nil {
			return total, fmt.Errorf("todo.sqlite.MarkDoneOlderThan: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("todo.sqlite.MarkDoneOlderThan: rows affected: %w", err)
		}
		total += int(n)
		if int(n) < batch {
			return total, nil
		}
	}
}
