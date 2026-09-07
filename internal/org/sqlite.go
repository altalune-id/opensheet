package org

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	sqliteent "altalune.id/template/internal/platform/db/entity/sqlite"
)

type sqliteStore struct {
	db      *sql.DB
	orgs    *sqliteent.Orgs
	members *sqliteent.Memberships
	users   *sqliteent.Users
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:      db,
		orgs:    sqliteent.NewOrgs(tablePrefix),
		members: sqliteent.NewMemberships(tablePrefix),
		users:   sqliteent.NewUsers(tablePrefix),
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
	OrgID     string `alias:"memberships.org_id"`
	UserID    string `alias:"memberships.user_id"`
	Role      string `alias:"memberships.role"`
	CreatedAt string `alias:"memberships.created_at"`
	System    int64  `alias:"memberships.system"`
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
	return &Membership{
		OrgID:     orgID,
		UserID:    userID,
		Role:      Role(r.Role),
		CreatedAt: ca,
		System:    r.System != 0,
	}, nil
}

type sqliteMemberProfileRow struct {
	UserID    string `alias:"memberships.user_id"`
	Role      string `alias:"memberships.role"`
	CreatedAt string `alias:"memberships.created_at"`
	System    int64  `alias:"memberships.system"`
	Email     string `alias:"users.email"`
	Name      string `alias:"users.name"`
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
	return &MemberProfile{
		UserID:    userID,
		Email:     r.Email,
		Name:      r.Name,
		Role:      Role(r.Role),
		CreatedAt: ca,
		System:    r.System != 0,
	}, nil
}

func (s *sqliteStore) Save(ctx context.Context, o *Org) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	created := o.CreatedAt.UTC().Format(time.RFC3339Nano)
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
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
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

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Org, error) {
	return s.queryOrg(ctx, s.orgs.ID.EQ(sqlite.String(id.String())), &NotFoundError{ID: id.String()})
}

func (s *sqliteStore) List(ctx context.Context, userID uuid.UUID) ([]*Org, error) {
	stmt := sqlite.SELECT(s.orgs.ID, s.orgs.Slug, s.orgs.Name, s.orgs.CreatedBy, s.orgs.CreatedAt, s.orgs.System).
		FROM(s.orgs.INNER_JOIN(s.members, s.members.OrgID.EQ(s.orgs.ID))).
		WHERE(s.members.UserID.EQ(sqlite.String(userID.String()))).
		ORDER_BY(s.orgs.CreatedAt.ASC())
	var rows []sqliteOrgRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
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
	stmt := s.members.INSERT(s.members.AllColumns).
		VALUES(
			uuid.New().String(),
			m.OrgID.String(),
			m.UserID.String(),
			string(m.Role),
			m.CreatedAt.UTC().Format(time.RFC3339Nano),
			boolToInt(m.System),
		).
		ON_CONFLICT(s.members.OrgID, s.members.UserID).
		DO_UPDATE(sqlite.SET(
			s.members.Role.SET(sqlite.String(string(m.Role))),
			s.members.System.SET(sqlite.Int64(boolToInt(m.System))),
		))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("org.sqlite: SaveMembership: %w", err)
	}
	return nil
}

func (s *sqliteStore) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*Membership, error) {
	stmt := sqlite.SELECT(s.members.OrgID, s.members.UserID, s.members.Role, s.members.CreatedAt, s.members.System).
		FROM(s.members).
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.members.UserID.EQ(sqlite.String(userID.String())))).
		LIMIT(1)
	var row sqliteMembershipRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
		}
		return nil, fmt.Errorf("org.sqlite: MembershipOf: %w", err)
	}
	return row.toMembership()
}

func (s *sqliteStore) ListMembers(ctx context.Context, orgID uuid.UUID) ([]*Membership, error) {
	stmt := sqlite.SELECT(s.members.OrgID, s.members.UserID, s.members.Role, s.members.CreatedAt, s.members.System).
		FROM(s.members).
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String()))).
		ORDER_BY(s.members.CreatedAt.ASC())
	var rows []sqliteMembershipRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
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

func (s *sqliteStore) ListMemberProfiles(ctx context.Context, orgID uuid.UUID) ([]*MemberProfile, error) {
	stmt := sqlite.SELECT(
		s.members.UserID,
		s.members.Role,
		s.members.CreatedAt,
		s.members.System,
		s.users.Email,
		s.users.Name,
	).
		FROM(s.members.INNER_JOIN(s.users, s.users.ID.EQ(s.members.UserID))).
		WHERE(s.members.OrgID.EQ(sqlite.String(orgID.String()))).
		ORDER_BY(s.members.CreatedAt.ASC())
	var rows []sqliteMemberProfileRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
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
	res, err := stmt.ExecContext(ctx, s.db)
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
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
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
