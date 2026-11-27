package fakes

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
)

type orgMembers interface {
	MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*org.Membership, error)
	ByID(ctx context.Context, id uuid.UUID) (*org.Org, error)
}

// OrgRoles is a role source for authz.NewResolver over a real org service, mapped as boot maps it: no membership or no org is role "".
type OrgRoles struct {
	Orgs orgMembers
}

// RoleOf returns userID's role in orgID, "" for a non-member or a missing org, and any other store error raw.
func (a OrgRoles) RoleOf(ctx context.Context, orgID, userID uuid.UUID) (role string, roleID uuid.UUID, orgName string, grant session.Grant, err error) {
	m, err := a.Orgs.MembershipOf(ctx, orgID, userID)
	if org.IsMembershipMissingError(err) {
		return "", uuid.Nil, "", session.Grant{}, nil
	}
	if err != nil {
		return "", uuid.Nil, "", session.Grant{}, err
	}
	o, err := a.Orgs.ByID(ctx, orgID)
	if org.IsNotFoundError(err) {
		return "", uuid.Nil, "", session.Grant{}, nil
	}
	if err != nil {
		return "", uuid.Nil, "", session.Grant{}, err
	}
	return string(m.Role), uuid.Nil, o.Name, session.Grant{OrgID: orgID, All: true}, nil
}
