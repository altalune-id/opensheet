package apikey

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db     *sql.DB
	table  *sqliteent.APIKeys
	grants *sqliteent.APIKeySheets
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:     db,
		table:  sqliteent.NewAPIKeys(tablePrefix),
		grants: sqliteent.NewAPIKeySheets(tablePrefix),
	}
}

type sqliteAPIKeyRow struct {
	ID         string  `alias:"api_keys.id"`
	OrgID      string  `alias:"api_keys.org_id"`
	ProjectID  string  `alias:"api_keys.project_id"`
	Name       string  `alias:"api_keys.name"`
	KeyPrefix  string  `alias:"api_keys.key_prefix"`
	SecretHash []byte  `alias:"api_keys.secret_hash"`
	Scopes     string  `alias:"api_keys.scopes"`
	ExpiresAt  *string `alias:"api_keys.expires_at"`
	LastUsedAt *string `alias:"api_keys.last_used_at"`
	RevokedAt  *string `alias:"api_keys.revoked_at"`
	CreatedAt  string  `alias:"api_keys.created_at"`
}

func (r *sqliteAPIKeyRow) toAPIKey() (*APIKey, error) {
	id, err := parseSQLiteUUID("id", r.ID)
	if err != nil {
		return nil, err
	}
	orgID, err := parseSQLiteUUID("org_id", r.OrgID)
	if err != nil {
		return nil, err
	}
	projectID, err := parseSQLiteUUID("project_id", r.ProjectID)
	if err != nil {
		return nil, err
	}
	scopes, err := decodeScopes(r.Scopes)
	if err != nil {
		return nil, fmt.Errorf("apikey.sqlite: scopes: %w", err)
	}
	createdAt, err := parseSQLiteTime("created_at", r.CreatedAt)
	if err != nil {
		return nil, err
	}
	expiresAt, err := parseSQLiteTimePtr("expires_at", r.ExpiresAt)
	if err != nil {
		return nil, err
	}
	lastUsedAt, err := parseSQLiteTimePtr("last_used_at", r.LastUsedAt)
	if err != nil {
		return nil, err
	}
	revokedAt, err := parseSQLiteTimePtr("revoked_at", r.RevokedAt)
	if err != nil {
		return nil, err
	}
	return &APIKey{
		ID:         id,
		OrgID:      orgID,
		ProjectID:  projectID,
		Name:       r.Name,
		KeyPrefix:  r.KeyPrefix,
		SecretHash: r.SecretHash,
		Scopes:     scopes,
		SheetIDs:   []uuid.UUID{},
		ExpiresAt:  expiresAt,
		LastUsedAt: lastUsedAt,
		RevokedAt:  revokedAt,
		CreatedAt:  createdAt,
	}, nil
}

type sqliteGrantRow struct {
	APIKeyID string `alias:"api_key_sheets.api_key_id"`
	SheetID  string `alias:"api_key_sheets.sheet_id"`
}

func (s *sqliteStore) Save(ctx context.Context, k *APIKey) error {
	if _, err := tenant.From(ctx); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := s.writeKey(ctx, tx, k); err != nil {
			return err
		}
		return s.writeGrant(ctx, tx, k)
	})
}

// SECURITY: a key read through PublicColumns carries no hash, so that case updates in place instead of
// upserting a NULL secret_hash over a live key.
func (s *sqliteStore) writeKey(ctx context.Context, tx *sql.Tx, k *APIKey) error {
	scopes, err := encodeScopes(k.Scopes)
	if err != nil {
		return err
	}
	if len(k.SecretHash) == 0 {
		return s.updateKey(ctx, tx, k, scopes)
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			k.ID.String(), k.OrgID.String(), k.ProjectID.String(), k.Name, k.KeyPrefix, k.SecretHash,
			scopes,
			sqliteNullableTime(k.ExpiresAt), sqliteNullableTime(k.LastUsedAt), sqliteNullableTime(k.RevokedAt),
			k.CreatedAt.UTC().Format(time.RFC3339Nano),
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(sqlite.SET(
			s.table.Name.SET(sqlite.String(k.Name)),
			s.table.Scopes.SET(sqlite.String(scopes)),
			s.table.ExpiresAt.SET(sqliteNullableTimeExpr(k.ExpiresAt)),
			s.table.LastUsedAt.SET(sqliteNullableTimeExpr(k.LastUsedAt)),
			s.table.RevokedAt.SET(sqliteNullableTimeExpr(k.RevokedAt)),
		))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		if isSQLiteUniqueViolation(err) {
			return &AlreadyExistsError{Field: "key_prefix", Value: k.KeyPrefix}
		}
		return fmt.Errorf("apikey.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) updateKey(ctx context.Context, tx *sql.Tx, k *APIKey, scopes string) error {
	stmt := s.table.
		UPDATE(s.table.Name, s.table.Scopes, s.table.ExpiresAt, s.table.LastUsedAt, s.table.RevokedAt).
		SET(
			sqlite.String(k.Name),
			sqlite.String(scopes),
			sqliteNullableTimeExpr(k.ExpiresAt),
			sqliteNullableTimeExpr(k.LastUsedAt),
			sqliteNullableTimeExpr(k.RevokedAt),
		).
		WHERE(s.table.ID.EQ(sqlite.String(k.ID.String())).
			AND(s.table.OrgID.EQ(sqlite.String(k.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("apikey.sqlite.Save: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apikey.sqlite.Save: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: k.ID.String()}
	}
	return nil
}

func (s *sqliteStore) writeGrant(ctx context.Context, tx *sql.Tx, k *APIKey) error {
	del := s.grants.DELETE().
		WHERE(s.grants.APIKeyID.EQ(sqlite.String(k.ID.String())))
	if _, err := del.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.sqlite.Save: clear grant: %w", err)
	}
	if len(k.SheetIDs) == 0 {
		return nil
	}
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range k.SheetIDs {
		ins = ins.VALUES(k.ID.String(), id.String(), k.OrgID.String())
	}
	if _, err := ins.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.sqlite.Save: write grant: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	cols := s.publicProjection()
	stmt := sqlite.SELECT(cols[0], cols[1:]...).
		FROM(s.table).
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		LIMIT(1)
	var row sqliteAPIKeyRow
	if qErr := stmt.QueryContext(ctx, s.db, &row); qErr != nil {
		if isNoRows(qErr) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("apikey.sqlite.ByID: %w", qErr)
	}
	k, err := row.toAPIKey()
	if err != nil {
		return nil, err
	}
	return k, s.attachGrants(ctx, []*APIKey{k})
}

// SECURITY: resolves a presented key before any tenant scope exists; SQLite has no RLS, so this is a plain unfiltered read.
func (s *sqliteStore) ByPrefix(ctx context.Context, prefix string) (*APIKey, error) {
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.KeyPrefix.EQ(sqlite.String(prefix))).
		LIMIT(1)
	var row sqliteAPIKeyRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if isNoRows(err) {
			return nil, &NotFoundError{}
		}
		return nil, fmt.Errorf("apikey.sqlite.ByPrefix: %w", err)
	}
	k, err := row.toAPIKey()
	if err != nil {
		return nil, err
	}
	return k, s.attachGrants(ctx, []*APIKey{k})
}

func (s *sqliteStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*APIKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return []*APIKey{}, nil
	}
	cols := s.publicProjection()
	stmt := sqlite.SELECT(cols[0], cols[1:]...).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.ProjectID.EQ(sqlite.String(projectID.String())))).
		ORDER_BY(s.table.CreatedAt.DESC())
	var rows []sqliteAPIKeyRow
	if qErr := stmt.QueryContext(ctx, s.db, &rows); qErr != nil {
		return nil, fmt.Errorf("apikey.sqlite.List: %w", qErr)
	}
	out := make([]*APIKey, 0, len(rows))
	for i := range rows {
		k, cErr := rows[i].toAPIKey()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, k)
	}
	return out, s.attachGrants(ctx, out)
}

func (s *sqliteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		del := s.grants.DELETE().
			WHERE(s.grants.APIKeyID.EQ(sqlite.String(id.String())).
				AND(s.grants.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
		if _, dErr := del.ExecContext(ctx, tx); dErr != nil {
			return fmt.Errorf("apikey.sqlite.Delete: clear grant: %w", dErr)
		}
		stmt := s.table.DELETE().
			WHERE(s.table.ID.EQ(sqlite.String(id.String())).
				AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
		res, eErr := stmt.ExecContext(ctx, tx)
		if eErr != nil {
			return fmt.Errorf("apikey.sqlite.Delete: %w", eErr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("apikey.sqlite.Delete: rows affected: %w", raErr)
		}
		if n == 0 {
			return &NotFoundError{ID: id.String()}
		}
		return nil
	})
}

func (s *sqliteStore) attachGrants(ctx context.Context, keys []*APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]sqlite.Expression, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, sqlite.String(k.ID.String()))
	}
	stmt := sqlite.SELECT(s.grants.APIKeyID, s.grants.SheetID).
		FROM(s.grants).
		WHERE(s.grants.APIKeyID.IN(ids...)).
		ORDER_BY(s.grants.SheetID.ASC())
	var rows []sqliteGrantRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		if isNoRows(err) {
			return nil
		}
		return fmt.Errorf("apikey.sqlite.attachGrants: %w", err)
	}
	byKey := make(map[string][]uuid.UUID, len(keys))
	for i := range rows {
		sheetID, err := parseSQLiteUUID("sheet_id", rows[i].SheetID)
		if err != nil {
			return err
		}
		byKey[rows[i].APIKeyID] = append(byKey[rows[i].APIKeyID], sheetID)
	}
	for _, k := range keys {
		if got, ok := byKey[k.ID.String()]; ok {
			k.SheetIDs = got
		}
	}
	return nil
}

func (s *sqliteStore) publicProjection() []sqlite.Projection {
	return []sqlite.Projection{
		s.table.ID,
		s.table.OrgID,
		s.table.ProjectID,
		s.table.Name,
		s.table.KeyPrefix,
		s.table.Scopes,
		s.table.ExpiresAt,
		s.table.LastUsedAt,
		s.table.RevokedAt,
		s.table.CreatedAt,
	}
}

func (s *sqliteStore) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return fn(tx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apikey.sqlite: begin: %w", err)
	}
	if fnErr := fn(tx); fnErr != nil {
		_ = tx.Rollback()
		return fnErr
	}
	if cErr := tx.Commit(); cErr != nil {
		return fmt.Errorf("apikey.sqlite: commit: %w", cErr)
	}
	return nil
}

func sqliteNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sqliteNullableTimeExpr(t *time.Time) sqlite.StringExpression {
	if t == nil {
		return sqlite.StringExp(sqlite.NULL)
	}
	return sqlite.String(t.UTC().Format(time.RFC3339Nano))
}

func parseSQLiteUUID(col, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("apikey.sqlite: parse %s: %w", col, err)
	}
	return id, nil
}

func parseSQLiteTime(col, raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("apikey.sqlite: parse %s: %w", col, err)
	}
	return t.UTC(), nil
}

func parseSQLiteTimePtr(col string, raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil //nolint:nilnil // absent nullable timestamp
	}
	t, err := parseSQLiteTime(col, *raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func isSQLiteUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
