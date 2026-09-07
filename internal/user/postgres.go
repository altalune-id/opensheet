package user

import (
	"context"
	"errors"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/template/internal/platform/db"
	pgent "altalune.id/template/internal/platform/db/entity/postgres"
)

type postgresStore struct {
	pool  pdb.Pool
	table *pgent.Users
}

func newPostgresStore(pool pdb.Pool, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, table: pgent.NewUsers(schema, tablePrefix)}
}

type pgUserRow struct {
	ID              uuid.UUID  `alias:"users.id"`
	Email           string     `alias:"users.email"`
	Name            string     `alias:"users.name"`
	IsAdmin         bool       `alias:"users.is_admin"`
	IDPIssuer       *string    `alias:"users.idp_issuer"`
	PasswordHash    string     `alias:"users.password_hash"`
	Locale          string     `alias:"users.locale"`
	TermsAcceptedAt *time.Time `alias:"users.terms_accepted_at"`
	CreatedAt       time.Time  `alias:"users.created_at"`
}

func (r *pgUserRow) toUser() *User {
	u := &User{
		ID:           r.ID,
		Email:        r.Email,
		Name:         r.Name,
		IsAdmin:      r.IsAdmin,
		PasswordHash: r.PasswordHash,
		Locale:       r.Locale,
		CreatedAt:    r.CreatedAt,
	}
	hasIDPIssuer := r.IDPIssuer != nil && *r.IDPIssuer != ""
	u.Source = sourceFrom(r.IsAdmin, hasIDPIssuer, r.PasswordHash != "")
	if r.TermsAcceptedAt != nil {
		t := r.TermsAcceptedAt.UTC()
		u.TermsAcceptedAt = &t
	}
	return u
}

func (s *postgresStore) writer(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.pool.W
}

func (s *postgresStore) reader(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.pool.R
}

func sourceFrom(isAdmin, hasIDPIssuer, hasPassword bool) string {
	if hasIDPIssuer {
		return SourceOIDC
	}
	if hasPassword {
		return SourceLocal
	}
	if isAdmin {
		return SourceGenesis
	}
	return SourceGenesis
}

func isPGUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
