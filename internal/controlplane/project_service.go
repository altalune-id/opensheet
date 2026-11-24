package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
)

// ProjectService implements project.v1.ProjectService.
type ProjectService struct {
	projects *project.Service
	orgs     orgDirectory
}

type orgDirectory interface {
	orgLister
	ByID(ctx context.Context, id uuid.UUID) (*org.Org, error)
}

// NewProjectService binds the handler to its collaborators.
func NewProjectService(projects *project.Service, orgs orgDirectory) *ProjectService {
	return &ProjectService{projects: projects, orgs: orgs}
}

// ListProjects returns the projects the principal reaches: in its active org, or in every org of a person who has none. SECURITY: the orgs come from the principal and its memberships alone, so no caller can re-target this read.
func (s *ProjectService) ListProjects(ctx context.Context, _ *connect.Request[projectv1.ListProjectsRequest]) (*connect.Response[projectv1.ListProjectsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	orgs, err := s.orgsOf(ctx, p)
	if err != nil {
		return nil, err
	}
	resp := &projectv1.ListProjectsResponse{Projects: []*projectv1.Project{}}
	for _, o := range orgs {
		pinned := p
		pinned.ActiveOrgID = o.ID
		items, err := s.projects.List(tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: p.UserID}), o.ID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if !pinned.ReachesProject(item.OrgID, item.ID) {
				continue
			}
			pb := projectToProto(item)
			pb.OrgSlug = o.Slug
			resp.Projects = append(resp.Projects, pb)
		}
	}
	return connect.NewResponse(resp), nil
}

// SECURITY: only a person with no active org widens to its memberships; a key stays in the org it was minted for.
func (s *ProjectService) orgsOf(ctx context.Context, p session.Principal) ([]*org.Org, error) {
	if p.Source != session.SourceAPIKey && p.ActiveOrgID == uuid.Nil {
		return s.orgs.List(ctx, p.UserID)
	}
	o, err := s.orgs.ByID(tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID}), p.ActiveOrgID)
	if err != nil {
		return nil, err
	}
	return []*org.Org{o}, nil
}

func projectToProto(p *project.Project) *projectv1.Project {
	return &projectv1.Project{
		Id:        p.ID.String(),
		OrgId:     p.OrgID.String(),
		Slug:      p.Slug,
		Name:      p.Name,
		System:    p.System,
		CreatedAt: timestamppb.New(p.CreatedAt),
	}
}
