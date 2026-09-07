package org

import (
	"context"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
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
	stmt := s.members.INSERT(s.members.AllColumns).
		VALUES(uuid.New(), m.OrgID, m.UserID, string(m.Role), m.CreatedAt.UTC(), m.System).
		ON_CONFLICT(s.members.OrgID, s.members.UserID).
		DO_UPDATE(postgres.SET(
			s.members.Role.SET(postgres.String(string(m.Role))),
			s.members.System.SET(postgres.Bool(m.System)),
		))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("org.postgres: SaveMembership: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
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
