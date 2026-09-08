package credential

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresStore struct {
	pool  pdb.Pool
	pc    *tenant.PgConn
	table *pgent.Credentials
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, table: pgent.NewCredentials(schema, tablePrefix)}
}

type pgCredentialRow struct {
	ID                 uuid.UUID `alias:"credentials.id"`
	OrgID              uuid.UUID `alias:"credentials.org_id"`
	ProjectID          uuid.UUID `alias:"credentials.project_id"`
	Name               string    `alias:"credentials.name"`
	Kind               string    `alias:"credentials.kind"`
	Status             string    `alias:"credentials.status"`
	AuthorizedByUserID uuid.UUID `alias:"credentials.authorized_by_user_id"`
	GoogleAccountEmail string    `alias:"credentials.google_account_email"`
	Sealed             []byte    `alias:"credentials.sealed"`
	CreatedAt          time.Time `alias:"credentials.created_at"`
	UpdatedAt          time.Time `alias:"credentials.updated_at"`
}

func (r *pgCredentialRow) toCredential() *Credential {
	return &Credential{
		ID:                 r.ID,
		OrgID:              r.OrgID,
		ProjectID:          r.ProjectID,
		Name:               r.Name,
		Kind:               Kind(r.Kind),
		Status:             Status(r.Status),
		AuthorizedByUserID: r.AuthorizedByUserID,
		GoogleAccountEmail: r.GoogleAccountEmail,
		Sealed:             r.Sealed,
		CreatedAt:          r.CreatedAt.UTC(),
		UpdatedAt:          r.UpdatedAt.UTC(),
	}
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, fmt.Errorf("credential.postgres: begin: %w", err)
	}
	return tx, true, nil
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
		return fmt.Errorf("credential.postgres: commit: %w", cerr)
	}
	return nil
}
