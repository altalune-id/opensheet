package fakes

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	customrole "altalune.id/opensheet/internal/role"
)

type orgMembers interface {
	MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*org.Membership, error)
	ByID(ctx context.Context, id uuid.UUID) (*org.Org, error)
}

type rolePermissions interface {
	Permissions(ctx context.Context, roleID uuid.UUID) ([]customrole.Permission, error)
}

// OrgRoles is a role source for authz.NewResolver over a real org service, mapped as boot maps it: no membership or no org is role "".
type OrgRoles struct {
	Orgs  orgMembers
	Roles rolePermissions
}

// RoleOf returns userID's role and custom role id in orgID, "" for a non-member or a missing org, and any other store error raw.
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
	return string(m.Role), m.RoleID, o.Name, session.Grant{OrgID: orgID, All: true}, nil
}

// CustomPermissions reads roleID's permissions from Roles; without Roles every custom role is *customrole.NotFoundError.
func (a OrgRoles) CustomPermissions(ctx context.Context, roleID uuid.UUID) ([]authz.Permission, error) {
	if a.Roles == nil {
		return nil, &customrole.NotFoundError{ID: roleID.String()}
	}
	return a.Roles.Permissions(ctx, roleID)
}
