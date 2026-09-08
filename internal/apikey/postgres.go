package apikey

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

// NOTE: the definer wrapper is a set-returning function in FROM position, which go-jet cannot build;
// the column aliases mirror what jet emits for a real table so pgAPIKeyRow still maps.
// NOTE: scopes is text[] and go-jet v2 has no array column type, so every read projects it as a JSON string.
const apiKeyFuncSelect = `SELECT k.id AS "api_keys.id", k.org_id AS "api_keys.org_id", ` +
	`k.project_id AS "api_keys.project_id", k.name AS "api_keys.name", ` +
	`k.key_prefix AS "api_keys.key_prefix", k.secret_hash AS "api_keys.secret_hash", ` +
	`to_json(k.scopes)::text AS "api_keys.scopes", k.expires_at AS "api_keys.expires_at", ` +
	`k.last_used_at AS "api_keys.last_used_at", k.revoked_at AS "api_keys.revoked_at", ` +
	`k.created_at AS "api_keys.created_at" FROM `

const scopesAsJSON = `to_json(scopes)::text`

type postgresStore struct {
	pool         pdb.Pool
	pc           *tenant.PgConn
	table        *pgent.APIKeys
	grants       *pgent.APIKeySheets
	byPrefixStmt string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	if schema == "" {
		schema = "public"
	}
	return &postgresStore{
		pool:         pool,
		pc:           pc,
		table:        pgent.NewAPIKeys(schema, tablePrefix),
		grants:       pgent.NewAPIKeySheets(schema, tablePrefix),
		byPrefixStmt: apiKeyFuncSelect + schema + "." + tablePrefix + "apikey_by_prefix(#prefix) AS k",
	}
}

type pgAPIKeyRow struct {
	ID         uuid.UUID  `alias:"api_keys.id"`
	OrgID      uuid.UUID  `alias:"api_keys.org_id"`
	ProjectID  uuid.UUID  `alias:"api_keys.project_id"`
	Name       string     `alias:"api_keys.name"`
	KeyPrefix  string     `alias:"api_keys.key_prefix"`
	SecretHash []byte     `alias:"api_keys.secret_hash"`
	Scopes     string     `alias:"api_keys.scopes"`
	ExpiresAt  *time.Time `alias:"api_keys.expires_at"`
	LastUsedAt *time.Time `alias:"api_keys.last_used_at"`
	RevokedAt  *time.Time `alias:"api_keys.revoked_at"`
	CreatedAt  time.Time  `alias:"api_keys.created_at"`
}

func (r *pgAPIKeyRow) toAPIKey() (*APIKey, error) {
	scopes, err := decodeScopes(r.Scopes)
	if err != nil {
		return nil, fmt.Errorf("apikey.postgres: scopes: %w", err)
	}
	return &APIKey{
		ID:         r.ID,
		OrgID:      r.OrgID,
		ProjectID:  r.ProjectID,
		Name:       r.Name,
		KeyPrefix:  r.KeyPrefix,
		SecretHash: r.SecretHash,
		Scopes:     scopes,
		SheetIDs:   []uuid.UUID{},
		ExpiresAt:  utcOrNil(r.ExpiresAt),
		LastUsedAt: utcOrNil(r.LastUsedAt),
		RevokedAt:  utcOrNil(r.RevokedAt),
		CreatedAt:  r.CreatedAt.UTC(),
	}, nil
}

type pgGrantRow struct {
	APIKeyID uuid.UUID `alias:"api_key_sheets.api_key_id"`
	SheetID  uuid.UUID `alias:"api_key_sheets.sheet_id"`
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
		return nil, false, fmt.Errorf("apikey.postgres: begin: %w", err)
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
		return fmt.Errorf("apikey.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) publicProjection() []postgres.Projection {
	return []postgres.Projection{
		s.table.ID,
		s.table.OrgID,
		s.table.ProjectID,
		s.table.Name,
		s.table.KeyPrefix,
		postgres.Raw(scopesAsJSON).AS("api_keys.scopes"),
		s.table.ExpiresAt,
		s.table.LastUsedAt,
		s.table.RevokedAt,
		s.table.CreatedAt,
	}
}

func decodeScopes(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func encodeScopes(scopes []string) (string, error) {
	if len(scopes) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(scopes)
	if err != nil {
		return "", fmt.Errorf("apikey: scopes: %w", err)
	}
	return string(b), nil
}

// SECURITY: pgx encodes a non-nil []string as text[]; a nil slice would land as NULL and violate the column's NOT NULL.
func scopeArray(scopes []string) []string {
	if scopes == nil {
		return []string{}
	}
	return scopes
}

func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func pgNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func pgNullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return postgres.TimestampzExp(postgres.NULL)
	}
	return postgres.TimestampzT(t.UTC())
}

func isPostgresUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}
