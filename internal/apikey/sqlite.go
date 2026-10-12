package apikey

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db         *sql.DB
	keys       *sqliteent.APIKeys
	grants     *sqliteent.APIKeyProjects
	grantsExpr sqlite.Expression
}

func newSQLiteStore(sqlDB *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:     sqlDB,
		keys:   sqliteent.NewAPIKeys(tablePrefix),
		grants: sqliteent.NewAPIKeyProjects(tablePrefix),
		// NOTE: Raw because go-jet has no builder for a correlated json_group_array subquery in a projection.
		grantsExpr: sqlite.Raw("COALESCE((SELECT json_group_array(o.project_id) FROM (SELECT g.project_id FROM " + tablePrefix +
			"api_key_projects g WHERE g.org_id = api_keys.org_id AND g.key_id = api_keys.id ORDER BY g.created_at, g.project_id) o), '[]')"),
	}
}

type sqliteKeyRow struct {
	ID          string  `alias:"api_keys.id"`
	OrgID       string  `alias:"api_keys.org_id"`
	ProjectID   *string `alias:"api_keys.project_id"`
	Kind        string  `alias:"api_keys.kind"`
	AllProjects bool    `alias:"api_keys.all_projects"`
	ProjectIDs  string  `alias:"api_keys.project_ids"`
	Name        string  `alias:"api_keys.name"`
	SecretHash  []byte  `alias:"api_keys.secret_hash"`
	SecretHint  string  `alias:"api_keys.secret_hint"`
	Scopes      string  `alias:"api_keys.scopes"`
	ResourceIDs string  `alias:"api_keys.resource_ids"`
	CreatedBy   *string `alias:"api_keys.created_by"`
	CreatedAt   string  `alias:"api_keys.created_at"`
	ExpiresAt   *string `alias:"api_keys.expires_at"`
	RevokedAt   *string `alias:"api_keys.revoked_at"`
	LastUsedAt  *string `alias:"api_keys.last_used_at"`
}

func (r *sqliteKeyRow) toAPIKey() (*APIKey, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse org_id: %w", err)
	}
	pid, err := parseNullableUUID(r.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse project_id: %w", err)
	}
	createdBy, err := parseNullableUUID(r.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse created_by: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse created_at: %w", err)
	}
	var scopes []string
	if err := json.Unmarshal([]byte(r.Scopes), &scopes); err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse scopes: %w", err)
	}
	var resourceIDs []uuid.UUID
	if err := json.Unmarshal([]byte(r.ResourceIDs), &resourceIDs); err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse resource_ids: %w", err)
	}
	projectIDs := []uuid.UUID{}
	if err := json.Unmarshal([]byte(r.ProjectIDs), &projectIDs); err != nil {
		return nil, fmt.Errorf("apikey.sqlite: parse project_ids: %w", err)
	}
	var hash [32]byte
	copy(hash[:], r.SecretHash)
	k := &APIKey{
		ID:          id,
		OrgID:       oid,
		ProjectID:   pid,
		Kind:        Kind(r.Kind),
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
		Name:        r.Name,
		SecretHash:  hash,
		SecretHint:  r.SecretHint,
		Scopes:      scopes,
		ResourceIDs: resourceIDs,
		CreatedBy:   createdBy,
		CreatedAt:   ca,
	}
	if r.ExpiresAt != nil && *r.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339Nano, *r.ExpiresAt)
		if perr != nil {
			return nil, fmt.Errorf("apikey.sqlite: parse expires_at: %w", perr)
		}
		k.ExpiresAt = &t
	}
	if r.RevokedAt != nil && *r.RevokedAt != "" {
		t, perr := time.Parse(time.RFC3339Nano, *r.RevokedAt)
		if perr != nil {
			return nil, fmt.Errorf("apikey.sqlite: parse revoked_at: %w", perr)
		}
		k.RevokedAt = &t
	}
	if r.LastUsedAt != nil && *r.LastUsedAt != "" {
		t, perr := time.Parse(time.RFC3339Nano, *r.LastUsedAt)
		if perr != nil {
			return nil, fmt.Errorf("apikey.sqlite: parse last_used_at: %w", perr)
		}
		k.LastUsedAt = &t
	}
	return k, nil
}

// NOTE: enrolls in the caller's unit of work, so a Save never opens a second SQLite writer transaction.
func (s *sqliteStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, tc, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("apikey.sqlite: begin: %w", err)
	}
	return tx, true, tc, nil
}

func (s *sqliteStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("apikey.sqlite: commit: %w", cerr)
	}
	return nil
}

func (s *sqliteStore) Save(ctx context.Context, k *APIKey) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveWithGrants(ctx, tx, tc, k))
}

func (s *sqliteStore) saveWithGrants(ctx context.Context, tx *sql.Tx, tc tenant.Context, k *APIKey) error {
	if err := s.save(ctx, tx, tc, k); err != nil {
		return err
	}
	return s.syncGrants(ctx, tx, tc, k)
}

// SECURITY: SQLite has no RLS, so every statement names the caller's org and the composite foreign keys refuse another org's key or project.
func (s *sqliteStore) syncGrants(ctx context.Context, tx *sql.Tx, tc tenant.Context, k *APIKey) error {
	keep := make([]sqlite.Expression, 0, len(k.ProjectIDs))
	for _, id := range k.ProjectIDs {
		keep = append(keep, sqlite.String(id.String()))
	}
	where := s.grants.OrgID.EQ(sqlite.String(tc.OrgID.String())).AND(s.grants.KeyID.EQ(sqlite.String(k.ID.String())))
	if len(keep) > 0 {
		where = where.AND(s.grants.ProjectID.NOT_IN(keep...))
	}
	if _, err := s.grants.DELETE().WHERE(where).ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.sqlite.Save: prune grants: %w", err)
	}
	if len(k.ProjectIDs) == 0 {
		return nil
	}
	now := sqliteent.SQLiteTime(time.Now().UTC())
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range k.ProjectIDs {
		ins = ins.VALUES(tc.OrgID.String(), k.ID.String(), id.String(), now)
	}
	if _, err := ins.ON_CONFLICT(s.grants.KeyID, s.grants.ProjectID).DO_NOTHING().ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.sqlite.Save: add grants: %w", err)
	}
	return nil
}

func (s *sqliteStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, k *APIKey) error {
	scopesJSON, err := jsonArray(k.Scopes)
	if err != nil {
		return fmt.Errorf("apikey.sqlite.Save: encode scopes: %w", err)
	}
	resourceIDsJSON, err := jsonArray(k.ResourceIDs)
	if err != nil {
		return fmt.Errorf("apikey.sqlite.Save: encode resource_ids: %w", err)
	}
	// SECURITY: the conflict clause is guarded by org; SQLite has no RLS behind this.
	stmt := s.keys.INSERT(s.keys.AllColumns).
		VALUES(
			k.ID.String(),
			k.OrgID.String(),
			sqliteNullableUUIDArg(k.ProjectID),
			string(k.Kind),
			k.AllProjects,
			k.Name,
			k.SecretHash[:],
			k.SecretHint,
			scopesJSON,
			resourceIDsJSON,
			sqliteNullableUUIDArg(k.CreatedBy),
			sqliteent.SQLiteTime(k.CreatedAt),
			sqliteNullableTimeArg(k.ExpiresAt),
			sqliteNullableTimeArg(k.RevokedAt),
			sqliteNullableTimeArg(k.LastUsedAt),
		).
		ON_CONFLICT(s.keys.ID).
		DO_UPDATE(
			sqlite.SET(
				s.keys.Name.SET(sqlite.String(k.Name)),
				s.keys.AllProjects.SET(sqlite.Bool(k.AllProjects)),
				s.keys.Scopes.SET(sqlite.String(scopesJSON)),
				s.keys.ResourceIDs.SET(sqlite.String(resourceIDsJSON)),
				s.keys.ExpiresAt.SET(sqliteNullableTimeExpr(k.ExpiresAt)),
				s.keys.RevokedAt.SET(sqliteNullableTimeExpr(k.RevokedAt)),
				s.keys.LastUsedAt.SET(sqliteNullableTimeExpr(k.LastUsedAt)),
			).WHERE(s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String()))),
		)
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return fmt.Errorf("apikey.sqlite.Save: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("apikey.sqlite.Save: rows affected: %w", raErr)
	}
	if n == 0 {
		return &NotFoundError{}
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	return s.queryOne(ctx, tx,
		s.keys.ID.EQ(sqlite.String(id.String())).
			AND(s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String()))),
	)
}

// SECURITY: unauthenticated lookup; the hash is unguessable, SQLite has no RLS, and every follow-up action re-checks org membership once the key names its org.
func (s *sqliteStore) BySecretHash(ctx context.Context, hash [32]byte) (*APIKey, error) {
	execer := qrm.DB(s.db)
	if tx, ok := pdb.CurrentTx(ctx); ok {
		execer = tx
	}
	return s.queryOne(ctx, execer, s.keys.SecretHash.EQ(sqlite.Blob(hash[:])))
}

func (s *sqliteStore) queryOne(ctx context.Context, execer qrm.DB, cond sqlite.BoolExpression) (*APIKey, error) {
	stmt := sqlite.SELECT(s.keys.AllColumns, s.grantsExpr.AS("api_keys.project_ids")).
		FROM(s.keys).
		WHERE(cond).
		LIMIT(1)
	var row sqliteKeyRow
	if err := stmt.QueryContext(ctx, execer, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{}
		}
		return nil, fmt.Errorf("apikey.sqlite.query: %w", err)
	}
	return row.toAPIKey()
}

func (s *sqliteStore) List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	return s.queryMany(ctx, tx, "List", s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String())).
		AND(s.keys.Kind.EQ(sqlite.String(string(KindProject)))).
		AND(s.keys.ProjectID.EQ(sqlite.String(projectID.String()))))
}

func (s *sqliteStore) ListOrg(ctx context.Context) ([]*APIKey, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	return s.queryMany(ctx, tx, "ListOrg", s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String())).
		AND(s.keys.Kind.EQ(sqlite.String(string(KindOrg)))))
}

func (s *sqliteStore) ListPersonal(ctx context.Context, ownerID uuid.UUID) ([]*APIKey, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	return s.queryMany(ctx, tx, "ListPersonal", s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String())).
		AND(s.keys.Kind.EQ(sqlite.String(string(KindPersonal)))).
		AND(s.keys.CreatedBy.EQ(sqlite.String(ownerID.String()))))
}

func (s *sqliteStore) queryMany(ctx context.Context, tx *sql.Tx, op string, cond sqlite.BoolExpression) ([]*APIKey, error) {
	stmt := sqlite.SELECT(s.keys.AllColumns, s.grantsExpr.AS("api_keys.project_ids")).
		FROM(s.keys).
		WHERE(cond).
		ORDER_BY(s.keys.CreatedAt.DESC(), s.keys.ID.DESC())
	var rows []sqliteKeyRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("apikey.sqlite.%s: %w", op, qErr)
	}
	out := make([]*APIKey, 0, len(rows))
	for i := range rows {
		k, cErr := rows[i].toAPIKey()
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, k)
	}
	return out, nil
}

func (s *sqliteStore) RevokePersonal(ctx context.Context, ownerID uuid.UUID, at time.Time) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.keys.UPDATE(s.keys.RevokedAt).
		SET(sqlite.String(sqliteent.SQLiteTime(at))).
		WHERE(s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String())).
			AND(s.keys.Kind.EQ(sqlite.String(string(KindPersonal)))).
			AND(s.keys.CreatedBy.EQ(sqlite.String(ownerID.String()))).
			AND(s.keys.RevokedAt.IS_NULL()))
	_, err = stmt.ExecContext(ctx, tx)
	if err != nil {
		err = fmt.Errorf("apikey.sqlite.RevokePersonal: %w", err)
	}
	return s.endTx(tx, owned, err)
}

func (s *sqliteStore) TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.touchLastUsed(ctx, tx, tc, id, at))
}

func (s *sqliteStore) touchLastUsed(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, at time.Time) error {
	stmt := s.keys.UPDATE(s.keys.LastUsedAt).
		SET(sqlite.String(sqliteent.SQLiteTime(at))).
		WHERE(s.keys.ID.EQ(sqlite.String(id.String())).
			AND(s.keys.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("apikey.sqlite.TouchLastUsed: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("apikey.sqlite.TouchLastUsed: rows affected: %w", raErr)
	}
	if n == 0 {
		return &NotFoundError{}
	}
	return nil
}

func jsonArray[T any](v []T) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func sqliteNullableUUIDArg(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func parseNullableUUID(raw *string) (uuid.UUID, error) {
	if raw == nil || *raw == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(*raw)
}

func sqliteNullableTimeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return sqliteent.SQLiteTime(*t)
}

func sqliteNullableTimeExpr(t *time.Time) sqlite.StringExpression {
	if t == nil {
		return sqliteent.NullText()
	}
	return sqlite.String(sqliteent.SQLiteTime(*t))
}
