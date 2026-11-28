package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	orgv1 "altalune.id/opensheet/gen/go/org/v1"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
)

// MemberService implements org.v1.MemberService.
type MemberService struct {
	authz *authz.Resolver
	orgs  *org.Service
}

// NewMemberService binds the handler to its collaborators.
func NewMemberService(rz *authz.Resolver, orgs *org.Service) *MemberService {
	return &MemberService{authz: rz, orgs: orgs}
}

// ListMembers returns the members of the principal's own org. SECURITY: the org is read from the principal alone, so no caller can re-target this read.
func (s *MemberService) ListMembers(ctx context.Context, _ *connect.Request[orgv1.ListMembersRequest]) (*connect.Response[orgv1.ListMembersResponse], error) {
	ctx, p, err := principal(ctx, s.authz)
	if err != nil {
		return nil, err
	}
	items, err := s.orgs.ListMemberProfiles(tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID}), p.ActiveOrgID)
	if err != nil {
		return nil, err
	}
	// SECURITY: which projects a restricted member reaches is shown only to a caller who manages members org-wide.
	manager := authz.CanOrgWide(ctx, authz.MembersWrite) == nil
	resp := &orgv1.ListMembersResponse{Members: make([]*orgv1.Member, 0, len(items))}
	for _, m := range items {
		pb := &orgv1.Member{
			UserId:      m.UserID.String(),
			Email:       m.Email,
			Name:        m.Name,
			Role:        string(m.Role),
			JoinedAt:    timestamppb.New(m.CreatedAt),
			AllProjects: m.AllProjects || m.Role == org.RoleOwner,
		}
		if manager && !pb.AllProjects {
			for _, id := range m.ProjectIDs {
				pb.ProjectIds = append(pb.ProjectIds, id.String())
			}
		}
		resp.Members = append(resp.Members, pb)
	}
	return connect.NewResponse(resp), nil
}
