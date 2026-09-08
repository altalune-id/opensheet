package apikey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	cols := s.publicProjection()
	stmt := postgres.SELECT(cols[0], cols[1:]...).
		FROM(s.table).
		WHERE(s.table.ID.EQ(postgres.UUID(id))).
		LIMIT(1)
	var row pgAPIKeyRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if isNoRows(qErr) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("apikey.postgres.ByID: %w", qErr)
	}
	k, err := row.toAPIKey()
	if err != nil {
		return nil, err
	}
	return k, s.attachGrants(ctx, tx, []*APIKey{k})
}

func (s *postgresStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*APIKey, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	cols := s.publicProjection()
	stmt := postgres.SELECT(cols[0], cols[1:]...).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.table.ProjectID.EQ(postgres.UUID(projectID)))).
		ORDER_BY(s.table.CreatedAt.DESC())
	var rows []pgAPIKeyRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("apikey.postgres.List: %w", qErr)
	}
	out := make([]*APIKey, 0, len(rows))
	for i := range rows {
		k, cErr := rows[i].toAPIKey()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, k)
	}
	return out, s.attachGrants(ctx, tx, out)
}

// SECURITY: resolves a presented key before any tenant scope exists; the 008 SECURITY DEFINER wrapper is what lifts RLS, not the caller's role.
func (s *postgresStore) ByPrefix(ctx context.Context, prefix string) (*APIKey, error) {
	var rows []pgAPIKeyRow
	stmt := postgres.RawStatement(s.byPrefixStmt, postgres.RawArgs{"#prefix": prefix})
	if err := stmt.QueryContext(ctx, s.execer(ctx), &rows); err != nil {
		if isNoRows(err) {
			return nil, &NotFoundError{}
		}
		return nil, fmt.Errorf("apikey.postgres.ByPrefix: %w", err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{}
	}
	k, err := rows[0].toAPIKey()
	if err != nil {
		return nil, err
	}
	// SECURITY: the grant rows sit behind RLS and an empty grant means "every sheet in the project", so they must be
	// read under the key's own org rather than defaulted — a filtered-out grant would widen the key.
	tx, err := s.pc.BeginTenanted(ctx, tenant.Context{OrgID: k.OrgID, ProjectID: k.ProjectID})
	if err != nil {
		return nil, fmt.Errorf("apikey.postgres.ByPrefix: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return k, s.attachGrants(ctx, tx, []*APIKey{k})
}

func (s *postgresStore) attachGrants(ctx context.Context, execer qrm.DB, keys []*APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]postgres.Expression, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, postgres.UUID(k.ID))
	}
	stmt := postgres.SELECT(s.grants.APIKeyID, s.grants.SheetID).
		FROM(s.grants).
		WHERE(s.grants.APIKeyID.IN(ids...)).
		ORDER_BY(s.grants.SheetID.ASC())
	var rows []pgGrantRow
	if err := stmt.QueryContext(ctx, execer, &rows); err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("apikey.postgres.attachGrants: %w", err)
	}
	byKey := make(map[uuid.UUID][]uuid.UUID, len(keys))
	for i := range rows {
		byKey[rows[i].APIKeyID] = append(byKey[rows[i].APIKeyID], rows[i].SheetID)
	}
	for _, k := range keys {
		if got, ok := byKey[k.ID]; ok {
			k.SheetIDs = got
		}
	}
	return nil
}

func (s *postgresStore) execer(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.pc.DB
}

func isNoRows(err error) bool {
	return errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows)
}
