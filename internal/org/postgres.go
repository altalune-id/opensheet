package org

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

// NOTE: the definer wrappers are set-returning functions in FROM position, which go-jet cannot build; the column aliases mirror what jet emits for a real table so pgOrgRow still maps.
const orgFuncSelect = `SELECT o.id AS "orgs.id", o.slug AS "orgs.slug", o.name AS "orgs.name", ` +
	`o.created_by AS "orgs.created_by", o.created_at AS "orgs.created_at", o.system AS "orgs.system" FROM `

type postgresStore struct {
	pool                 pdb.Pool
	pc                   *tenant.PgConn
	orgs                 *pgent.Orgs
	members              *pgent.Memberships
	grants               *pgent.MembershipProjects
	grantsExpr           postgres.Expression
	users                *pgent.Users
	resolveBySlugStmt    string
	resolveSystemOrgStmt string
	listForUserStmt      string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	if schema == "" {
		schema = "public"
	}
	fnPrefix := schema + "." + tablePrefix
	return &postgresStore{
		pool:    pool,
		pc:      pc,
		orgs:    pgent.NewOrgs(schema, tablePrefix),
		members: pgent.NewMemberships(schema, tablePrefix),
		grants:  pgent.NewMembershipProjects(schema, tablePrefix),
		// NOTE: Raw because go-jet has no builder for a correlated array_agg subquery in a projection.
		grantsExpr: postgres.Raw("COALESCE((SELECT array_agg(g.project_id ORDER BY g.created_at, g.project_id) FROM " + fnPrefix +
			"membership_projects g WHERE g.org_id = memberships.org_id AND g.membership_id = memberships.id), '{}')::text"),
		users:                pgent.NewUsers(schema, tablePrefix),
		resolveBySlugStmt:    orgFuncSelect + fnPrefix + "resolve_org_by_slug(#slug) AS o",
		resolveSystemOrgStmt: orgFuncSelect + fnPrefix + "resolve_system_org() AS o",
		// NOTE: a SELECT without ORDER BY has no guaranteed row order, whatever ordering the wrapper body carries.
		listForUserStmt: orgFuncSelect + fnPrefix + "list_orgs_for_user(#userID) AS o" +
			" ORDER BY o.created_at ASC, o.id ASC",
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
		CreatedAt: r.CreatedAt.UTC(),
		System:    r.System,
	}
}

type pgMembershipRow struct {
	OrgID       uuid.UUID  `alias:"memberships.org_id"`
	UserID      uuid.UUID  `alias:"memberships.user_id"`
	Role        string     `alias:"memberships.role"`
	RoleID      *uuid.UUID `alias:"memberships.role_id"`
	CreatedAt   time.Time  `alias:"memberships.created_at"`
	System      bool       `alias:"memberships.system"`
	AllProjects bool       `alias:"memberships.all_projects"`
	ProjectIDs  string     `alias:"memberships.project_ids"`
}

func (r *pgMembershipRow) toMembership() (*Membership, error) {
	projectIDs, err := parsePgUUIDArray(r.ProjectIDs)
	if err != nil {
		return nil, fmt.Errorf("org.postgres: parse project_ids: %w", err)
	}
	return &Membership{
		OrgID:       r.OrgID,
		UserID:      r.UserID,
		Role:        Role(r.Role),
		RoleID:      derefUUID(r.RoleID),
		CreatedAt:   r.CreatedAt.UTC(),
		System:      r.System,
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
	}, nil
}

type pgMemberProfileRow struct {
	UserID      uuid.UUID  `alias:"memberships.user_id"`
	Role        string     `alias:"memberships.role"`
	RoleID      *uuid.UUID `alias:"memberships.role_id"`
	CreatedAt   time.Time  `alias:"memberships.created_at"`
	System      bool       `alias:"memberships.system"`
	Email       string     `alias:"users.email"`
	Name        string     `alias:"users.name"`
	AllProjects bool       `alias:"memberships.all_projects"`
	ProjectIDs  string     `alias:"memberships.project_ids"`
}

func (r *pgMemberProfileRow) toProfile() (*MemberProfile, error) {
	projectIDs, err := parsePgUUIDArray(r.ProjectIDs)
	if err != nil {
		return nil, fmt.Errorf("org.postgres: parse project_ids: %w", err)
	}
	return &MemberProfile{
		UserID:      r.UserID,
		Email:       r.Email,
		Name:        r.Name,
		Role:        Role(r.Role),
		RoleID:      derefUUID(r.RoleID),
		CreatedAt:   r.CreatedAt.UTC(),
		System:      r.System,
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
	}, nil
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

func isPostgresOneSystemViolation(err error, index string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasSuffix(pgErr.ConstraintName, index)
}

func isPostgresUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}

func parsePgUUIDArray(raw string) ([]uuid.UUID, error) {
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "{"), "}")
	if body == "" {
		return []uuid.UUID{}, nil
	}
	parts := strings.Split(body, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func derefUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
