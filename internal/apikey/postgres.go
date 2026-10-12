package apikey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresStore struct {
	pool             pdb.Pool
	pc               *tenant.PgConn
	keys             *pgent.APIKeys
	grants           *pgent.APIKeyProjects
	grantsExpr       postgres.Expression
	bySecretHashStmt string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	if schema == "" {
		schema = "public"
	}
	cols := `k.id AS "api_keys.id", k.org_id AS "api_keys.org_id", k.project_id AS "api_keys.project_id", ` +
		`k.kind AS "api_keys.kind", k.all_projects AS "api_keys.all_projects", k.project_ids AS "api_keys.project_ids", ` +
		`k.name AS "api_keys.name", k.secret_hash AS "api_keys.secret_hash", k.secret_hint AS "api_keys.secret_hint", ` +
		`k.scopes AS "api_keys.scopes", k.resource_ids AS "api_keys.resource_ids", k.created_by AS "api_keys.created_by", ` +
		`k.created_at AS "api_keys.created_at", k.expires_at AS "api_keys.expires_at", ` +
		`k.revoked_at AS "api_keys.revoked_at", k.last_used_at AS "api_keys.last_used_at"`
	fn := schema + "." + tablePrefix
	return &postgresStore{
		pool:   pool,
		pc:     pc,
		keys:   pgent.NewAPIKeys(schema, tablePrefix),
		grants: pgent.NewAPIKeyProjects(schema, tablePrefix),
		// NOTE: Raw because go-jet has no builder for a correlated array_agg subquery in a projection.
		grantsExpr: postgres.Raw("COALESCE((SELECT array_agg(g.project_id ORDER BY g.created_at, g.project_id) FROM " + fn +
			"api_key_projects g WHERE g.org_id = api_keys.org_id AND g.key_id = api_keys.id), '{}')::text"),
		// NOTE: RawStatement because go-jet has no builder for a set-returning function in FROM position.
		bySecretHashStmt: "SELECT " + cols + " FROM " + fn + "resolve_api_key_by_secret_hash(#hash) k",
	}
}

type pgKeyRow struct {
	ID          uuid.UUID  `alias:"api_keys.id"`
	OrgID       uuid.UUID  `alias:"api_keys.org_id"`
	ProjectID   *uuid.UUID `alias:"api_keys.project_id"`
	Kind        string     `alias:"api_keys.kind"`
	AllProjects bool       `alias:"api_keys.all_projects"`
	ProjectIDs  string     `alias:"api_keys.project_ids"`
	Name        string     `alias:"api_keys.name"`
	SecretHash  []byte     `alias:"api_keys.secret_hash"`
	SecretHint  string     `alias:"api_keys.secret_hint"`
	Scopes      string     `alias:"api_keys.scopes"`
	ResourceIDs string     `alias:"api_keys.resource_ids"`
	CreatedBy   *uuid.UUID `alias:"api_keys.created_by"`
	CreatedAt   time.Time  `alias:"api_keys.created_at"`
	ExpiresAt   *time.Time `alias:"api_keys.expires_at"`
	RevokedAt   *time.Time `alias:"api_keys.revoked_at"`
	LastUsedAt  *time.Time `alias:"api_keys.last_used_at"`
}

func (r *pgKeyRow) toAPIKey() (*APIKey, error) {
	scopes, err := parsePgTextArray(r.Scopes)
	if err != nil {
		return nil, fmt.Errorf("apikey.postgres: parse scopes: %w", err)
	}
	resourceIDs, err := parsePgUUIDArray(r.ResourceIDs)
	if err != nil {
		return nil, fmt.Errorf("apikey.postgres: parse resource_ids: %w", err)
	}
	projectIDs, err := parsePgUUIDArray(r.ProjectIDs)
	if err != nil {
		return nil, fmt.Errorf("apikey.postgres: parse project_ids: %w", err)
	}
	var hash [32]byte
	copy(hash[:], r.SecretHash)
	k := &APIKey{
		ID:          r.ID,
		OrgID:       r.OrgID,
		Kind:        Kind(r.Kind),
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
		Name:        r.Name,
		SecretHash:  hash,
		SecretHint:  r.SecretHint,
		Scopes:      scopes,
		ResourceIDs: resourceIDs,
		CreatedAt:   r.CreatedAt.UTC(),
	}
	if r.ProjectID != nil {
		k.ProjectID = *r.ProjectID
	}
	if r.CreatedBy != nil {
		k.CreatedBy = *r.CreatedBy
	}
	if r.ExpiresAt != nil {
		t := r.ExpiresAt.UTC()
		k.ExpiresAt = &t
	}
	if r.RevokedAt != nil {
		t := r.RevokedAt.UTC()
		k.RevokedAt = &t
	}
	if r.LastUsedAt != nil {
		t := r.LastUsedAt.UTC()
		k.LastUsedAt = &t
	}
	return k, nil
}

func parsePgUUIDArray(raw string) ([]uuid.UUID, error) {
	strs, err := parsePgTextArray(raw)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(strs))
	for _, s := range strs {
		id, perr := uuid.Parse(s)
		if perr != nil {
			return nil, perr
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		tc, err := tenant.From(ctx)
		if err != nil {
			return nil, false, tenant.Context{}, err
		}
		return tx, false, tc, nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("apikey.postgres: begin: %w", err)
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
		return fmt.Errorf("apikey.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) selectColumns() postgres.ProjectionList {
	return postgres.ProjectionList{
		s.keys.ID, s.keys.OrgID, s.keys.ProjectID, s.keys.Kind, s.keys.AllProjects,
		s.grantsExpr.AS("api_keys.project_ids"),
		s.keys.Name, s.keys.SecretHash, s.keys.SecretHint,
		postgres.CAST(s.keys.Scopes).AS_TEXT().AS("api_keys.scopes"),
		postgres.CAST(s.keys.ResourceIDs).AS_TEXT().AS("api_keys.resource_ids"),
		s.keys.CreatedBy, s.keys.CreatedAt, s.keys.ExpiresAt, s.keys.RevokedAt, s.keys.LastUsedAt,
	}
}

func (s *postgresStore) Save(ctx context.Context, k *APIKey) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.saveWithGrants(ctx, tx, tc, k))
}

func (s *postgresStore) saveWithGrants(ctx context.Context, tx *sql.Tx, tc tenant.Context, k *APIKey) error {
	if err := s.save(ctx, tx, tc, k); err != nil {
		return err
	}
	return s.syncGrants(ctx, tx, tc, k)
}

// SECURITY: every statement names the caller's org, and the table's composite foreign keys refuse a row naming another org's key or project.
func (s *postgresStore) syncGrants(ctx context.Context, tx *sql.Tx, tc tenant.Context, k *APIKey) error {
	keep := make([]postgres.Expression, 0, len(k.ProjectIDs))
	for _, id := range k.ProjectIDs {
		keep = append(keep, postgres.UUID(id))
	}
	where := s.grants.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.grants.KeyID.EQ(postgres.UUID(k.ID)))
	if len(keep) > 0 {
		where = where.AND(s.grants.ProjectID.NOT_IN(keep...))
	}
	if _, err := s.grants.DELETE().WHERE(where).ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.postgres.Save: prune grants: %w", err)
	}
	if len(k.ProjectIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range k.ProjectIDs {
		ins = ins.VALUES(tc.OrgID, k.ID, id, now)
	}
	if _, err := ins.ON_CONFLICT(s.grants.KeyID, s.grants.ProjectID).DO_NOTHING().ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("apikey.postgres.Save: add grants: %w", err)
	}
	return nil
}

func (s *postgresStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, k *APIKey) error {
	// SECURITY: the conflict clause is guarded by org, or an upsert carrying another
	// tenant's row id would rewrite that row wherever RLS is inert.
	stmt := s.keys.INSERT(s.keys.AllColumns).
		VALUES(
			k.ID, k.OrgID, pgNullableUUID(k.ProjectID), string(k.Kind), k.AllProjects, k.Name, k.SecretHash[:], k.SecretHint,
			pgTextArrayExpr(k.Scopes), pgUUIDArrayExpr(k.ResourceIDs), pgNullableUUID(k.CreatedBy),
			k.CreatedAt.UTC(), pgNullableTime(k.ExpiresAt), pgNullableTime(k.RevokedAt), pgNullableTime(k.LastUsedAt),
		).
		ON_CONFLICT(s.keys.ID).
		DO_UPDATE(
			postgres.SET(
				s.keys.Name.SET(postgres.String(k.Name)),
				s.keys.AllProjects.SET(postgres.Bool(k.AllProjects)),
				s.keys.Scopes.SET(pgTextArrayExpr(k.Scopes)),
				s.keys.ResourceIDs.SET(pgUUIDArrayExpr(k.ResourceIDs)),
				s.keys.ExpiresAt.SET(pgNullableTimeExpr(k.ExpiresAt)),
				s.keys.RevokedAt.SET(pgNullableTimeExpr(k.RevokedAt)),
				s.keys.LastUsedAt.SET(pgNullableTimeExpr(k.LastUsedAt)),
			).WHERE(s.keys.OrgID.EQ(postgres.UUID(tc.OrgID))),
		)
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return fmt.Errorf("apikey.postgres.Save: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("apikey.postgres.Save: rows affected: %w", raErr)
	}
	// NOTE: an insert affects one row and so does a conflicting update the caller owns; zero
	// means the conflict-clause guard refused an upsert onto a row outside the caller's org.
	if n == 0 {
		return &NotFoundError{}
	}
	return nil
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.selectColumns()).
		FROM(s.keys).
		WHERE(s.keys.ID.EQ(postgres.UUID(id)).
			AND(s.keys.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1)
	var row pgKeyRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{}
		}
		return nil, fmt.Errorf("apikey.postgres.ByID: %w", qErr)
	}
	return row.toAPIKey()
}

// SECURITY: resolves a key by its secret hash before any tenant scope exists; the SECURITY DEFINER wrapper lifts RLS, and the hash is unguessable.
func (s *postgresStore) BySecretHash(ctx context.Context, hash [32]byte) (*APIKey, error) {
	execer := qrm.DB(s.pc.DB)
	if tx, ok := pdb.CurrentTx(ctx); ok {
		execer = tx
	}
	var rows []pgKeyRow
	if err := postgres.RawStatement(s.bySecretHashStmt, postgres.RawArgs{"#hash": hash[:]}).QueryContext(ctx, execer, &rows); err != nil {
		return nil, fmt.Errorf("apikey.postgres.BySecretHash: %w", err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{}
	}
	return rows[0].toAPIKey()
}

func (s *postgresStore) List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error) {
	return s.listWhere(ctx, "List", s.keys.Kind.EQ(postgres.String(string(KindProject))).AND(s.keys.ProjectID.EQ(postgres.UUID(projectID))))
}

func (s *postgresStore) ListOrg(ctx context.Context) ([]*APIKey, error) {
	return s.listWhere(ctx, "ListOrg", s.keys.Kind.EQ(postgres.String(string(KindOrg))))
}

func (s *postgresStore) ListPersonal(ctx context.Context, ownerID uuid.UUID) ([]*APIKey, error) {
	return s.listWhere(ctx, "ListPersonal", s.keys.Kind.EQ(postgres.String(string(KindPersonal))).AND(s.keys.CreatedBy.EQ(postgres.UUID(ownerID))))
}

// SECURITY: every listing is pinned to the caller's org here, whatever cond adds.
func (s *postgresStore) listWhere(ctx context.Context, op string, cond postgres.BoolExpression) ([]*APIKey, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.selectColumns()).
		FROM(s.keys).
		WHERE(s.keys.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(cond)).
		ORDER_BY(s.keys.CreatedAt.DESC(), s.keys.ID.DESC())
	return s.queryKeys(ctx, tx, stmt, op)
}

func (s *postgresStore) queryKeys(ctx context.Context, tx *sql.Tx, stmt postgres.SelectStatement, op string) ([]*APIKey, error) {
	var rows []pgKeyRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("apikey.postgres.%s: %w", op, qErr)
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

func (s *postgresStore) RevokePersonal(ctx context.Context, ownerID uuid.UUID, at time.Time) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.keys.UPDATE(s.keys.RevokedAt).
		SET(postgres.TimestampzT(at.UTC())).
		WHERE(s.keys.OrgID.EQ(postgres.UUID(tc.OrgID)).
			AND(s.keys.Kind.EQ(postgres.String(string(KindPersonal)))).
			AND(s.keys.CreatedBy.EQ(postgres.UUID(ownerID))).
			AND(s.keys.RevokedAt.IS_NULL()))
	_, err = stmt.ExecContext(ctx, tx)
	if err != nil {
		err = fmt.Errorf("apikey.postgres.RevokePersonal: %w", err)
	}
	return s.endTx(tx, owned, err)
}

func (s *postgresStore) TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.touchLastUsed(ctx, tx, tc, id, at))
}

func (s *postgresStore) touchLastUsed(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, at time.Time) error {
	stmt := s.keys.UPDATE(s.keys.LastUsedAt).
		SET(postgres.TimestampzT(at.UTC())).
		WHERE(s.keys.ID.EQ(postgres.UUID(id)).
			AND(s.keys.OrgID.EQ(postgres.UUID(tc.OrgID))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("apikey.postgres.TouchLastUsed: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("apikey.postgres.TouchLastUsed: rows affected: %w", raErr)
	}
	if n == 0 {
		return &NotFoundError{}
	}
	return nil
}

func pgNullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func pgNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func pgNullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return pgent.NullTimestampz()
	}
	return postgres.TimestampzT(t.UTC())
}

// NOTE: the cast to text[] is explicit because Postgres has no assignment cast from a text parameter to an array type.
func pgTextArrayExpr(elems []string) postgres.StringExpression {
	return postgres.StringExp(postgres.CAST(postgres.String(pgTextArrayLiteral(elems))).AS("text[]"))
}

func pgUUIDArrayExpr(ids []uuid.UUID) postgres.StringExpression {
	return postgres.StringExp(postgres.CAST(postgres.String(pgUUIDArrayLiteral(ids))).AS("uuid[]"))
}

func pgTextArrayLiteral(elems []string) string {
	if len(elems) == 0 {
		return "{}"
	}
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	parts := make([]string, len(elems))
	for i, e := range elems {
		parts[i] = `"` + esc.Replace(e) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func pgUUIDArrayLiteral(ids []uuid.UUID) string {
	elems := make([]string, len(ids))
	for i, id := range ids {
		elems[i] = id.String()
	}
	return pgTextArrayLiteral(elems)
}

func parsePgTextArray(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}, nil
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("malformed array literal %q", s)
	}
	body := s[1 : len(s)-1]
	if body == "" {
		return []string{}, nil
	}
	var out []string
	var cur strings.Builder
	inQuotes, escaped := false, false
	for _, r := range body {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	return out, nil
}
