package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

type sqliteStore struct {
	db         *sql.DB
	orgs       *sqliteent.Orgs
	members    *sqliteent.Memberships
	grants     *sqliteent.MembershipProjects
	grantsExpr sqlite.Expression
	users      *sqliteent.Users
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:      db,
		orgs:    sqliteent.NewOrgs(tablePrefix),
		members: sqliteent.NewMemberships(tablePrefix),
		grants:  sqliteent.NewMembershipProjects(tablePrefix),
		// SECURITY: SQLite has no RLS, so the org predicate is the only thing keeping another org's grant rows out.
		grantsExpr: sqlite.Raw("COALESCE((SELECT json_group_array(o.project_id) FROM (SELECT g.project_id FROM " + tablePrefix +
			"membership_projects g WHERE g.org_id = memberships.org_id AND g.membership_id = memberships.id ORDER BY g.created_at, g.project_id) o), '[]')"),
		users: sqliteent.NewUsers(tablePrefix),
	}
}

type sqliteOrgRow struct {
	ID        string `alias:"orgs.id"`
	Slug      string `alias:"orgs.slug"`
	Name      string `alias:"orgs.name"`
	CreatedBy string `alias:"orgs.created_by"`
	CreatedAt string `alias:"orgs.created_at"`
	System    int64  `alias:"orgs.system"`
}

func (r *sqliteOrgRow) toOrg() (*Org, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse id: %w", err)
	}
	owner, err := uuid.Parse(r.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse created_by: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse created_at: %w", err)
	}
	return &Org{
		ID:        id,
		Slug:      r.Slug,
		Name:      r.Name,
		OwnerID:   owner,
		CreatedAt: ca,
		System:    r.System != 0,
	}, nil
}

type sqliteMembershipRow struct {
	OrgID       string  `alias:"memberships.org_id"`
	UserID      string  `alias:"memberships.user_id"`
	Role        string  `alias:"memberships.role"`
	RoleID      *string `alias:"memberships.role_id"`
	CreatedAt   string  `alias:"memberships.created_at"`
	System      int64   `alias:"memberships.system"`
	AllProjects bool    `alias:"memberships.all_projects"`
	ProjectIDs  string  `alias:"memberships.project_ids"`
}

func (r *sqliteMembershipRow) toMembership() (*Membership, error) {
	orgID, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse org_id: %w", err)
	}
	userID, err := uuid.Parse(r.UserID)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse user_id: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse created_at: %w", err)
	}
	roleID, err := parseRoleID(r.RoleID)
	if err != nil {
		return nil, err
	}
	projectIDs := []uuid.UUID{}
	if err := json.Unmarshal([]byte(r.ProjectIDs), &projectIDs); err != nil {
		return nil, fmt.Errorf("org.sqlite: parse project_ids: %w", err)
	}
	return &Membership{
		OrgID:       orgID,
		UserID:      userID,
		Role:        Role(r.Role),
		RoleID:      roleID,
		CreatedAt:   ca,
		System:      r.System != 0,
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
	}, nil
}

type sqliteMemberProfileRow struct {
	UserID      string  `alias:"memberships.user_id"`
	Role        string  `alias:"memberships.role"`
	RoleID      *string `alias:"memberships.role_id"`
	CreatedAt   string  `alias:"memberships.created_at"`
	System      int64   `alias:"memberships.system"`
	Email       string  `alias:"users.email"`
	Name        string  `alias:"users.name"`
	AllProjects bool    `alias:"memberships.all_projects"`
	ProjectIDs  string  `alias:"memberships.project_ids"`
}

func (r *sqliteMemberProfileRow) toProfile() (*MemberProfile, error) {
	userID, err := uuid.Parse(r.UserID)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse user_id: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("org.sqlite: parse created_at: %w", err)
	}
	roleID, err := parseRoleID(r.RoleID)
	if err != nil {
		return nil, err
	}
	projectIDs := []uuid.UUID{}
	if err := json.Unmarshal([]byte(r.ProjectIDs), &projectIDs); err != nil {
		return nil, fmt.Errorf("org.sqlite: parse project_ids: %w", err)
	}
	return &MemberProfile{
		UserID:      userID,
		Email:       r.Email,
		Name:        r.Name,
		Role:        Role(r.Role),
		RoleID:      roleID,
		CreatedAt:   ca,
		System:      r.System != 0,
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
	}, nil
}

func (s *sqliteStore) Save(ctx context.Context, o *Org) error {
	now := sqliteent.SQLiteTime(time.Now())
	created := sqliteent.SQLiteTime(o.CreatedAt)
	stmt := s.orgs.INSERT(s.orgs.AllColumns).
		VALUES(o.ID.String(), o.Slug, o.Name, o.OwnerID.String(), created, now, boolToInt(o.System)).
		ON_CONFLICT(s.orgs.ID).
		DO_UPDATE(sqlite.SET(
			s.orgs.Slug.SET(sqlite.String(o.Slug)),
			s.orgs.Name.SET(sqlite.String(o.Name)),
			s.orgs.CreatedBy.SET(sqlite.String(o.OwnerID.String())),
			s.orgs.UpdatedAt.SET(sqlite.String(now)),
			s.orgs.System.SET(sqlite.Int64(boolToInt(o.System))),
		))
	if _, err := stmt.ExecContext(ctx, s.conn(ctx)); err != nil {
		if isSQLiteUniqueViolation(err, "orgs.system") {
			return &SystemOrgExistsError{Slug: o.Slug}
		}
		if isSQLiteUniqueViolation(err, "slug") {
			return &AlreadyExistsError{Slug: o.Slug}
		}
		return fmt.Errorf("org.sqlite: Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) BySlug(ctx context.Context, slug string) (*Org, error) {
	return s.queryOrg(ctx, s.orgs.Slug.EQ(sqlite.String(slug)), &NotFoundError{Slug: slug})
}

func (s *sqliteStore) SystemOrg(ctx context.Context) (*Org, error) {
	stmt := sqlite.SELECT(s.orgs.ID, s.orgs.Slug, s.orgs.Name, s.orgs.CreatedBy, s.orgs.CreatedAt, s.orgs.System).
		FROM(s.orgs).
		WHERE(s.orgs.System.EQ(sqlite.Int64(1))).
		ORDER_BY(s.orgs.CreatedAt.ASC(), s.orgs.ID.ASC()).
		LIMIT(1)
	var row sqliteOrgRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &NotFoundError{System: true}
		}
		return nil, fmt.Errorf("org.sqlite: SystemOrg: %w", err)
	}
	return row.toOrg()
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Org, error) {
	return s.queryOrg(ctx, s.orgs.ID.EQ(sqlite.String(id.String())), &NotFoundError{ID: id.String()})
}

func (s *sqliteStore) List(ctx context.Context, userID uuid.UUID) ([]*Org, error) {
	stmt := sqlite.SELECT(s.orgs.ID, s.orgs.Slug, s.orgs.Name, s.orgs.CreatedBy, s.orgs.CreatedAt, s.orgs.System).
		FROM(s.orgs.INNER_JOIN(s.members, s.members.OrgID.EQ(s.orgs.ID))).
		WHERE(s.members.UserID.EQ(sqlite.String(userID.String()))).
		ORDER_BY(s.orgs.CreatedAt.ASC(), s.orgs.ID.ASC())
	var rows []sqliteOrgRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &rows); err != nil {
		return nil, fmt.Errorf("org.sqlite: List: %w", err)
	}
	out := make([]*Org, 0, len(rows))
	for i := range rows {
		o, err := rows[i].toOrg()
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *sqliteStore) SaveMembership(ctx context.Context, m *Membership) error {
	cols := sqlite.ColumnList{s.members.ID, s.members.OrgID, s.members.UserID, s.members.Role, s.members.CreatedAt, s.members.System, s.members.RoleID}
	stmt := s.members.INSERT(cols).
		VALUES(
			uuid.New().String(),
			m.OrgID.String(),
			m.UserID.String(),
			string(m.Role),
			sqliteent.SQLiteTime(m.CreatedAt),
			boolToInt(m.System),
			sqliteNullableRoleID(m.RoleID),
		).
		ON_CONFLICT(s.members.OrgID, s.members.UserID).
		DO_UPDATE(sqlite.SET(
			s.members.Role.SET(sqlite.String(string(m.Role))),
			s.members.RoleID.SET(sqliteRoleIDExpr(m.RoleID)),
			s.members.System.SET(sqlite.Int64(boolToInt(m.System))),
		))
	if _, err := stmt.ExecContext(ctx, s.conn(ctx)); err != nil {
		return fmt.Errorf("org.sqlite: SaveMembership: %w", err)
	}
	return nil
}

func (s *sqliteStore) SetProjectGrant(ctx context.Context, orgID, userID uuid.UUID, g ProjectGrant) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.setProjectGrant(ctx, tx, orgID, userID, g))
}

func (s *sqliteStore) setProjectGrant(ctx context.Context, tx *sql.Tx, orgID, userID uuid.UUID, g ProjectGrant) error {
	byMember := s.members.OrgID.EQ(sqlite.String(orgID.String())).AND(s.members.UserID.EQ(sqlite.String(userID.String())))
	res, err := s.members.UPDATE(s.members.AllProjects).SET(sqlite.Bool(g.All)).WHERE(byMember).ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("org.sqlite: SetProjectGrant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("org.sqlite: SetProjectGrant: RowsAffected: %w", err)
	}
	if n == 0 {
		return &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
	}
	var row struct {
		ID string `alias:"memberships.id"`
	}
	if err := sqlite.SELECT(s.members.ID).FROM(s.members).WHERE(byMember).QueryContext(ctx, tx, &row); err != nil {
		return fmt.Errorf("org.sqlite: SetProjectGrant: read id: %w", err)
	}
	return s.syncGrants(ctx, tx, orgID, row.ID, g.ProjectIDs)
}

// SECURITY: SQLite has no RLS, so every statement names the membership's org and the composite foreign keys refuse another org's project.
func (s *sqliteStore) syncGrants(ctx context.Context, tx *sql.Tx, orgID uuid.UUID, membershipID string, projectIDs []uuid.UUID) error {
	keep := make([]sqlite.Expression, 0, len(projectIDs))
	for _, id := range projectIDs {
		keep = append(keep, sqlite.String(id.String()))
	}
	where := s.grants.OrgID.EQ(sqlite.String(orgID.String())).AND(s.grants.MembershipID.EQ(sqlite.String(membershipID)))
	if len(keep) > 0 {
		where = where.AND(s.grants.ProjectID.NOT_IN(keep...))
	}
	if _, err := s.grants.DELETE().WHERE(where).ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("org.sqlite: SetProjectGrant: prune grants: %w", err)
	}
	if len(projectIDs) == 0 {
		return nil
	}
	now := sqliteent.SQLiteTime(time.Now().UTC())
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range projectIDs {
		ins = ins.VALUES(orgID.String(), membershipID, id.String(), now)
	}
	if _, err := ins.ON_CONFLICT(s.grants.MembershipID, s.grants.ProjectID).DO_NOTHING().ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("org.sqlite: SetProjectGrant: add grants: %w", err)
	}
	return nil
}

// NOTE: enrolls in the caller's unit of work, so a save never opens a second SQLite writer transaction.
func (s *sqliteStore) txAcquire(ctx context.Context) (*sql.Tx, bool, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("org.sqlite: begin: %w", err)
	}
	return tx, true, nil
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
		return fmt.Errorf("org.sqlite: commit: %w", cerr)
	}
	return nil
}

func (s *sqliteStore) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*Membership, error) {
	stmt := sqlite.SELECT(s.members.OrgID, s.members.UserID, s.members.Role, s.members.RoleID, s.members.CreatedAt, s.members.System, s.members.AllProjects, s.grantsExpr.AS("memberships.project_ids")).
		FROM(s.members).
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.members.UserID.EQ(sqlite.String(userID.String())))).
		LIMIT(1)
	var row sqliteMembershipRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
		}
		return nil, fmt.Errorf("org.sqlite: MembershipOf: %w", err)
	}
	return row.toMembership()
}

func (s *sqliteStore) listMembersStmt(orgID uuid.UUID) sqlite.SelectStatement {
	return sqlite.SELECT(s.members.OrgID, s.members.UserID, s.members.Role, s.members.RoleID, s.members.CreatedAt, s.members.System, s.members.AllProjects, s.grantsExpr.AS("memberships.project_ids")).
		FROM(s.members).
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String()))).
		ORDER_BY(s.members.CreatedAt.ASC(), s.members.UserID.ASC())
}

func (s *sqliteStore) ListMembers(ctx context.Context, orgID uuid.UUID) ([]*Membership, error) {
	stmt := s.listMembersStmt(orgID)
	var rows []sqliteMembershipRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &rows); err != nil {
		return nil, fmt.Errorf("org.sqlite: ListMembers: %w", err)
	}
	out := make([]*Membership, 0, len(rows))
	for i := range rows {
		m, err := rows[i].toMembership()
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *sqliteStore) listMemberProfilesStmt(orgID uuid.UUID) sqlite.SelectStatement {
	return sqlite.SELECT(
		s.members.UserID,
		s.members.Role,
		s.members.RoleID,
		s.members.CreatedAt,
		s.members.System,
		s.users.Email,
		s.users.Name,
		s.members.AllProjects,
		s.grantsExpr.AS("memberships.project_ids"),
	).
		FROM(s.members.INNER_JOIN(s.users, s.users.ID.EQ(s.members.UserID))).
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String()))).
		ORDER_BY(s.members.CreatedAt.ASC(), s.members.UserID.ASC())
}

func (s *sqliteStore) ListMemberProfiles(ctx context.Context, orgID uuid.UUID) ([]*MemberProfile, error) {
	stmt := s.listMemberProfilesStmt(orgID)
	var rows []sqliteMemberProfileRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &rows); err != nil {
		return nil, fmt.Errorf("org.sqlite: ListMemberProfiles: %w", err)
	}
	out := make([]*MemberProfile, 0, len(rows))
	for i := range rows {
		p, err := rows[i].toProfile()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *sqliteStore) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	stmt := s.members.DELETE().
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.members.UserID.EQ(sqlite.String(userID.String()))))
	res, err := stmt.ExecContext(ctx, s.conn(ctx))
	if err != nil {
		return fmt.Errorf("org.sqlite: RemoveMember: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("org.sqlite: RemoveMember: RowsAffected: %w", err)
	}
	if n == 0 {
		return &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
	}
	return nil
}

func (s *sqliteStore) queryOrg(ctx context.Context, cond sqlite.BoolExpression, notFound *NotFoundError) (*Org, error) {
	stmt := sqlite.SELECT(s.orgs.ID, s.orgs.Slug, s.orgs.Name, s.orgs.CreatedBy, s.orgs.CreatedAt, s.orgs.System).
		FROM(s.orgs).
		WHERE(cond).
		LIMIT(1)
	var row sqliteOrgRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("org.sqlite: query: %w", err)
	}
	return row.toOrg()
}

func isSQLiteUniqueViolation(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") && strings.Contains(msg, column)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (s *sqliteStore) LockOrg(context.Context, uuid.UUID) error { return nil }

func (s *sqliteStore) conn(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.db
}

func parseRoleID(raw *string) (uuid.UUID, error) {
	if raw == nil || *raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("org.sqlite: parse role_id: %w", err)
	}
	return id, nil
}

func sqliteNullableRoleID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func sqliteRoleIDExpr(id uuid.UUID) sqlite.StringExpression {
	if id == uuid.Nil {
		return sqliteent.NullText()
	}
	return sqlite.String(id.String())
}
