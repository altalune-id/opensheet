package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
)

// ProjectService implements project.v1.ProjectService.
type ProjectService struct {
	projects *project.Service
}

// NewProjectService binds the handler to its collaborators.
func NewProjectService(projects *project.Service) *ProjectService {
	return &ProjectService{projects: projects}
}

// ListProjects returns the projects in the principal's active org that the principal reaches. SECURITY: the org is read from the principal alone, so no caller can re-target this read.
func (s *ProjectService) ListProjects(ctx context.Context, _ *connect.Request[projectv1.ListProjectsRequest]) (*connect.Response[projectv1.ListProjectsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	tctx := tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID})
	items, err := s.projects.List(tctx, p.ActiveOrgID)
	if err != nil {
		return nil, err
	}
	resp := &projectv1.ListProjectsResponse{Projects: make([]*projectv1.Project, 0, len(items))}
	for _, item := range items {
		if !p.ReachesProject(item.OrgID, item.ID) {
			continue
		}
		resp.Projects = append(resp.Projects, projectToProto(item))
	}
	return connect.NewResponse(resp), nil
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
