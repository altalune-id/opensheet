package boot

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

type authzRoleSource struct {
	orgs  orgMembers
	roles rolePermissions
}

func newAuthzResolver(orgs orgMembers, roles rolePermissions) *authz.Resolver {
	return authz.NewResolver(authzRoleSource{orgs: orgs, roles: roles})
}

func (a authzRoleSource) CustomPermissions(ctx context.Context, roleID uuid.UUID) ([]authz.Permission, error) {
	return a.roles.Permissions(ctx, roleID)
}

func (a authzRoleSource) RoleOf(ctx context.Context, orgID, userID uuid.UUID) (role string, roleID uuid.UUID, orgName string, grant session.Grant, err error) {
	m, err := a.orgs.MembershipOf(ctx, orgID, userID)
	if org.IsMembershipMissingError(err) {
		return "", uuid.Nil, "", session.Grant{}, nil
	}
	if err != nil {
		return "", uuid.Nil, "", session.Grant{}, err
	}
	o, err := a.orgs.ByID(ctx, orgID)
	if org.IsNotFoundError(err) {
		return "", uuid.Nil, "", session.Grant{}, nil
	}
	if err != nil {
		return "", uuid.Nil, "", session.Grant{}, err
	}
	return string(m.Role), m.RoleID, o.Name, memberGrant(orgID, m), nil
}

func memberGrant(orgID uuid.UUID, m *org.Membership) session.Grant {
	if m.AllProjects {
		return session.Grant{OrgID: orgID, All: true}
	}
	return session.Grant{OrgID: orgID, ProjectIDs: m.ProjectIDs}
}
