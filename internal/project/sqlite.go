package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	sqliteent "altalune.id/template/internal/platform/db/entity/sqlite"
	"altalune.id/template/internal/platform/tenant"
)

type sqliteStore struct {
	db    *sql.DB
	table *sqliteent.Projects
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewProjects(tablePrefix)}
}

type sqliteProjectRow struct {
	ID        string `alias:"projects.id"`
	OrgID     string `alias:"projects.org_id"`
	Slug      string `alias:"projects.slug"`
	Name      string `alias:"projects.name"`
	CreatedAt string `alias:"projects.created_at"`
	System    int64  `alias:"projects.system"`
}

func (r *sqliteProjectRow) toProject() (*Project, error) {
	pid, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("project.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("project.sqlite: parse org_id: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("project.sqlite: parse created_at: %w", err)
	}
	return &Project{
		ID:        pid,
		OrgID:     oid,
		Slug:      r.Slug,
		Name:      r.Name,
		CreatedAt: ca.UTC(),
		System:    r.System != 0,
	}, nil
}

func (s *sqliteStore) Save(ctx context.Context, p *Project) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sysVal := int64(0)
	if p.System {
		sysVal = 1
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			p.ID.String(), p.OrgID.String(), p.Slug, p.Name, tc.UserID.String(),
			p.CreatedAt.UTC().Format(time.RFC3339Nano),
			now, sysVal,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			sqlite.SET(
				s.table.Name.SET(sqlite.String(p.Name)),
				s.table.UpdatedAt.SET(sqlite.String(now)),
				s.table.System.SET(sqlite.Int64(sysVal)),
			),
		)
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		if isSQLiteUniqueViolation(err) {
			return &AlreadyExistsError{Field: "slug", Value: p.Slug}
		}
		return fmt.Errorf("project.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Project, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	return s.queryOne(ctx,
		s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))),
		&NotFoundError{ID: id.String()},
	)
}

func (s *sqliteStore) BySlug(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error) {
	if _, err := tenant.From(ctx); err != nil {
		return nil, err
	}
	return s.queryOne(ctx,
		s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.Slug.EQ(sqlite.String(slug))),
		&NotFoundError{OrgID: orgID.String(), Slug: slug},
	)
}

func (s *sqliteStore) List(ctx context.Context, orgID uuid.UUID) ([]*Project, error) {
	if _, err := tenant.From(ctx); err != nil {
		return nil, err
	}
	stmt := sqlite.SELECT(
		s.table.ID, s.table.OrgID, s.table.Slug, s.table.Name, s.table.CreatedAt, s.table.System,
	).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String()))).
		ORDER_BY(s.table.CreatedAt.ASC())
	var rows []sqliteProjectRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("project.sqlite.List: %w", err)
	}
	out := make([]*Project, 0, len(rows))
	for i := range rows {
		p, err := rows[i].toProject()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *sqliteStore) queryOne(ctx context.Context, cond sqlite.BoolExpression, notFound *NotFoundError) (*Project, error) {
	stmt := sqlite.SELECT(
		s.table.ID, s.table.OrgID, s.table.Slug, s.table.Name, s.table.CreatedAt, s.table.System,
	).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row sqliteProjectRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("project.sqlite.queryOne: %w", err)
	}
	return row.toProject()
}

func isSQLiteUniqueViolation(err error) bool {
	var sqliteErr *sqlitedrv.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return true
		}
	}
	return false
}
