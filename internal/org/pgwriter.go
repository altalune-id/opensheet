package org

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
)

func (s *postgresStore) Save(ctx context.Context, o *Org) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.orgs.INSERT(s.orgs.AllColumns).
		VALUES(o.ID, o.Slug, o.Name, o.OwnerID, o.CreatedAt.UTC(), now, o.System).
		ON_CONFLICT(s.orgs.ID).
		DO_UPDATE(postgres.SET(
			s.orgs.Slug.SET(postgres.String(o.Slug)),
			s.orgs.Name.SET(postgres.String(o.Name)),
			s.orgs.CreatedBy.SET(postgres.UUID(o.OwnerID)),
			s.orgs.UpdatedAt.SET(postgres.TimestampzT(now)),
			s.orgs.System.SET(postgres.Bool(o.System)),
		))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		if isPostgresOneSystemViolation(execErr, "orgs_one_system") {
			return s.endTx(tx, owned, &SystemOrgExistsError{Slug: o.Slug})
		}
		if isPostgresUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{Slug: o.Slug})
		}
		return s.endTx(tx, owned, fmt.Errorf("org.postgres: Save: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) SaveMembership(ctx context.Context, m *Membership) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	cols := postgres.ColumnList{s.members.ID, s.members.OrgID, s.members.UserID, s.members.Role, s.members.CreatedAt, s.members.System, s.members.RoleID}
	stmt := s.members.INSERT(cols).
		VALUES(uuid.New(), m.OrgID, m.UserID, string(m.Role), m.CreatedAt.UTC(), m.System, pgNullableRoleID(m.RoleID)).
		ON_CONFLICT(s.members.OrgID, s.members.UserID).
		DO_UPDATE(postgres.SET(
			s.members.Role.SET(postgres.String(string(m.Role))),
			s.members.RoleID.SET(pgRoleIDExpr(m.RoleID)),
			s.members.System.SET(postgres.Bool(m.System)),
		))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("org.postgres: SaveMembership: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) SetProjectGrant(ctx context.Context, orgID, userID uuid.UUID, g ProjectGrant) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.members.UPDATE(s.members.AllProjects).
		SET(postgres.Bool(g.All)).
		WHERE(s.members.OrgID.EQ(postgres.UUID(orgID)).AND(s.members.UserID.EQ(postgres.UUID(userID)))).
		RETURNING(s.members.ID)
	var row struct {
		ID uuid.UUID `alias:"memberships.id"`
	}
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return s.endTx(tx, owned, &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()})
		}
		return s.endTx(tx, owned, fmt.Errorf("org.postgres: SetProjectGrant: %w", qErr))
	}
	return s.endTx(tx, owned, s.syncGrants(ctx, tx, orgID, row.ID, g.ProjectIDs))
}

// SECURITY: every statement names the membership's org, so a BYPASSRLS role never touches another org's grant rows.
func (s *postgresStore) syncGrants(ctx context.Context, tx *sql.Tx, orgID, membershipID uuid.UUID, projectIDs []uuid.UUID) error {
	keep := make([]postgres.Expression, 0, len(projectIDs))
	for _, id := range projectIDs {
		keep = append(keep, postgres.UUID(id))
	}
	where := s.grants.OrgID.EQ(postgres.UUID(orgID)).AND(s.grants.MembershipID.EQ(postgres.UUID(membershipID)))
	if len(keep) > 0 {
		where = where.AND(s.grants.ProjectID.NOT_IN(keep...))
	}
	if _, err := s.grants.DELETE().WHERE(where).ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("org.postgres: SetProjectGrant: prune grants: %w", err)
	}
	if len(projectIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range projectIDs {
		ins = ins.VALUES(orgID, membershipID, id, now)
	}
	if _, err := ins.ON_CONFLICT(s.grants.MembershipID, s.grants.ProjectID).DO_NOTHING().ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("org.postgres: SetProjectGrant: add grants: %w", err)
	}
	return nil
}

func (s *postgresStore) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.members.DELETE().
		WHERE(s.members.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.members.UserID.EQ(postgres.UUID(userID))))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("org.postgres: RemoveMember: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("org.postgres: RemoveMember: RowsAffected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()})
	}
	return s.endTx(tx, owned, nil)
}

func pgNullableRoleID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func pgRoleIDExpr(id uuid.UUID) postgres.StringExpression {
	if id == uuid.Nil {
		return pgent.NullUUID()
	}
	return postgres.UUID(id)
}
