package credential

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db    *sql.DB
	table *sqliteent.Credentials
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewCredentials(tablePrefix)}
}

type sqliteCredentialRow struct {
	ID                 string `alias:"credentials.id"`
	OrgID              string `alias:"credentials.org_id"`
	ProjectID          string `alias:"credentials.project_id"`
	Name               string `alias:"credentials.name"`
	Kind               string `alias:"credentials.kind"`
	Status             string `alias:"credentials.status"`
	AuthorizedByUserID string `alias:"credentials.authorized_by_user_id"`
	GoogleAccountEmail string `alias:"credentials.google_account_email"`
	Sealed             []byte `alias:"credentials.sealed"`
	CreatedAt          string `alias:"credentials.created_at"`
	UpdatedAt          string `alias:"credentials.updated_at"`
}

func (r *sqliteCredentialRow) toCredential() (*Credential, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("credential.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("credential.sqlite: parse org_id: %w", err)
	}
	pid, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("credential.sqlite: parse project_id: %w", err)
	}
	aid, err := uuid.Parse(r.AuthorizedByUserID)
	if err != nil {
		return nil, fmt.Errorf("credential.sqlite: parse authorized_by_user_id: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("credential.sqlite: parse created_at: %w", err)
	}
	ua, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("credential.sqlite: parse updated_at: %w", err)
	}
	return &Credential{
		ID:                 id,
		OrgID:              oid,
		ProjectID:          pid,
		Name:               r.Name,
		Kind:               Kind(r.Kind),
		Status:             Status(r.Status),
		AuthorizedByUserID: aid,
		GoogleAccountEmail: r.GoogleAccountEmail,
		Sealed:             r.Sealed,
		CreatedAt:          ca.UTC(),
		UpdatedAt:          ua.UTC(),
	}, nil
}

func (s *sqliteStore) Save(ctx context.Context, c *Credential) error {
	if _, err := tenant.From(ctx); err != nil {
		return err
	}
	updatedAt := sqliteent.SQLiteTime(c.UpdatedAt)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			c.ID.String(),
			c.OrgID.String(),
			c.ProjectID.String(),
			c.Name,
			string(c.Kind),
			string(c.Status),
			c.AuthorizedByUserID.String(),
			c.GoogleAccountEmail,
			c.Sealed,
			sqliteent.SQLiteTime(c.CreatedAt),
			updatedAt,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			sqlite.SET(
				s.table.Name.SET(sqlite.String(c.Name)),
				s.table.Kind.SET(sqlite.String(string(c.Kind))),
				s.table.Status.SET(sqlite.String(string(c.Status))),
				s.table.GoogleAccountEmail.SET(sqlite.String(c.GoogleAccountEmail)),
				s.table.Sealed.SET(sqlite.Blob(c.Sealed)),
				s.table.UpdatedAt.SET(sqlite.String(updatedAt)),
			),
		)
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		if isSQLiteUniqueViolation(err) {
			return &AlreadyExistsError{Name: c.Name}
		}
		return fmt.Errorf("credential.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Credential, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		LIMIT(1)
	var row sqliteCredentialRow
	if qErr := stmt.QueryContext(ctx, s.db, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("credential.sqlite.ByID: %w", qErr)
	}
	return row.toCredential()
}

func (s *sqliteStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Credential, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return []*Credential{}, nil
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String())))).
		ORDER_BY(s.table.CreatedAt.DESC(), s.table.ID.DESC())
	var rows []sqliteCredentialRow
	if qErr := stmt.QueryContext(ctx, s.db, &rows); qErr != nil {
		return nil, fmt.Errorf("credential.sqlite.List: %w", qErr)
	}
	out := make([]*Credential, 0, len(rows))
	for i := range rows {
		c, cErr := rows[i].toCredential()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, c)
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
	res, execErr := stmt.ExecContext(ctx, s.db)
	if execErr != nil {
		if isSQLiteForeignKeyViolation(execErr) {
			return &InUseError{ID: id.String()}
		}
		return fmt.Errorf("credential.sqlite.Delete: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("credential.sqlite.Delete: rows affected: %w", raErr)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func isSQLiteUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isSQLiteForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
