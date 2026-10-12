package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	orgv1 "altalune.id/opensheet/gen/go/org/v1"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/tenant"
)

// MemberService implements org.v1.MemberService.
type MemberService struct {
	orgs *org.Service
}

// NewMemberService binds the handler to its collaborators.
func NewMemberService(orgs *org.Service) *MemberService {
	return &MemberService{orgs: orgs}
}

// ListMembers returns the members of the principal's own org. SECURITY: the org is read from the principal alone, so no caller can re-target this read.
func (s *MemberService) ListMembers(ctx context.Context, _ *connect.Request[orgv1.ListMembersRequest]) (*connect.Response[orgv1.ListMembersResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.orgs.ListMemberProfiles(tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID}), p.ActiveOrgID)
	if err != nil {
		return nil, err
	}
	resp := &orgv1.ListMembersResponse{Members: make([]*orgv1.Member, 0, len(items))}
	for _, m := range items {
		resp.Members = append(resp.Members, &orgv1.Member{
			UserId:   m.UserID.String(),
			Email:    m.Email,
			Name:     m.Name,
			Role:     string(m.Role),
			JoinedAt: timestamppb.New(m.CreatedAt),
		})
	}
	return connect.NewResponse(resp), nil
}
