package org

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/template/internal/platform/db"
	pgent "altalune.id/template/internal/platform/db/entity/postgres"
	"altalune.id/template/internal/platform/tenant"
)

// NOTE: the definer wrappers are set-returning functions in FROM position, which go-jet cannot build;
// the column aliases mirror what jet emits for a real table so pgOrgRow still maps.
const orgFuncSelect = `SELECT o.id AS "orgs.id", o.slug AS "orgs.slug", o.name AS "orgs.name", ` +
	`o.created_by AS "orgs.created_by", o.created_at AS "orgs.created_at", o.system AS "orgs.system" FROM `

type postgresStore struct {
	pool              pdb.Pool
	pc                *tenant.PgConn
	orgs              *pgent.Orgs
	members           *pgent.Memberships
	users             *pgent.Users
	resolveBySlugStmt string
	listForUserStmt   string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	if schema == "" {
		schema = "public"
	}
	fnPrefix := schema + "." + tablePrefix
	return &postgresStore{
		pool:              pool,
		pc:                pc,
		orgs:              pgent.NewOrgs(schema, tablePrefix),
		members:           pgent.NewMemberships(schema, tablePrefix),
		users:             pgent.NewUsers(schema, tablePrefix),
		resolveBySlugStmt: orgFuncSelect + fnPrefix + "resolve_org_by_slug(#slug) AS o",
		listForUserStmt:   orgFuncSelect + fnPrefix + "list_orgs_for_user(#userID) AS o",
	}
}

type pgOrgRow struct {
	ID        uuid.UUID `alias:"orgs.id"`
	Slug      string    `alias:"orgs.slug"`
	Name      string    `alias:"orgs.name"`
	CreatedBy uuid.UUID `alias:"orgs.created_by"`
	CreatedAt time.Time `alias:"orgs.created_at"`
	System    bool      `alias:"orgs.system"`
}

func (r *pgOrgRow) toOrg() *Org {
	return &Org{
		ID:        r.ID,
		Slug:      r.Slug,
		Name:      r.Name,
		OwnerID:   r.CreatedBy,
		CreatedAt: r.CreatedAt,
		System:    r.System,
	}
}

type pgMembershipRow struct {
	OrgID     uuid.UUID `alias:"memberships.org_id"`
	UserID    uuid.UUID `alias:"memberships.user_id"`
	Role      string    `alias:"memberships.role"`
	CreatedAt time.Time `alias:"memberships.created_at"`
	System    bool      `alias:"memberships.system"`
}

func (r *pgMembershipRow) toMembership() *Membership {
	return &Membership{
		OrgID:     r.OrgID,
		UserID:    r.UserID,
		Role:      Role(r.Role),
		CreatedAt: r.CreatedAt,
		System:    r.System,
	}
}

type pgMemberProfileRow struct {
	UserID    uuid.UUID `alias:"memberships.user_id"`
	Role      string    `alias:"memberships.role"`
	CreatedAt time.Time `alias:"memberships.created_at"`
	System    bool      `alias:"memberships.system"`
	Email     string    `alias:"users.email"`
	Name      string    `alias:"users.name"`
}

func (r *pgMemberProfileRow) toProfile() *MemberProfile {
	return &MemberProfile{
		UserID:    r.UserID,
		Email:     r.Email,
		Name:      r.Name,
		Role:      Role(r.Role),
		CreatedAt: r.CreatedAt,
		System:    r.System,
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
		return nil, false, fmt.Errorf("org.postgres: begin: %w", err)
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
		return fmt.Errorf("org.postgres: commit: %w", cerr)
	}
	return nil
}

func isPostgresUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}
