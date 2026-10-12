package controlplane

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
)

// SECURITY: a key principal carries UserID == uuid.Nil, so it is admitted on ActiveOrgID instead.
func principal(ctx context.Context) (session.Principal, error) {
	p := session.PrincipalFrom(ctx)
	usable := p.UserID != uuid.Nil
	if p.Source == session.SourceAPIKey {
		usable = p.ActiveOrgID != uuid.Nil
	}
	if !usable {
		return session.Principal{}, apperror.New(
			apperror.CodeUnauthenticated,
			"No principal in context",
			codes.Unauthenticated,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
		)
	}
	return p, nil
}

// SECURITY: the one place a handler turns a caller-supplied project id into a tenant scope; reach is decided by session.Principal alone.
func scopeToProject(ctx context.Context, projects *project.Service, projectIDRaw string) (context.Context, *project.Project, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, nil, err
	}
	pid, err := parseUUID("project_id", projectIDRaw)
	if err != nil {
		return nil, nil, err
	}
	proj, err := projects.ByID(tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID}), pid)
	if err != nil {
		return nil, nil, err
	}
	if !p.ReachesWholeProject(proj.OrgID, proj.ID) {
		return nil, nil, forbiddenErr("project is outside this credential's reach", "project_id", pid.String())
	}
	return bindTo(ctx, p, proj.OrgID, proj.ID), proj, nil
}

// NOTE: an omitted project id falls back to the principal's active project and still passes scopeToProject.
func scopeToActiveProject(ctx context.Context, projects *project.Service, projectIDRaw string) (context.Context, *project.Project, error) {
	if strings.TrimSpace(projectIDRaw) != "" {
		return scopeToProject(ctx, projects, projectIDRaw)
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, nil, err
	}
	if p.ActiveProjectID == uuid.Nil {
		return nil, nil, &ProjectUnresolvedError{}
	}
	return scopeToProject(ctx, projects, p.ActiveProjectID.String())
}

// SECURITY: binds ctx to a loaded row's project only when the principal reaches that row; deny is the row's own out-of-reach answer, so each entity keeps its absent-or-forbidden contract.
func scopeToResource(ctx context.Context, orgID, projectID, resourceID uuid.UUID, deny func() error) (context.Context, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if !p.ReachesResource(orgID, projectID, resourceID) {
		return nil, deny()
	}
	return bindTo(ctx, p, orgID, projectID), nil
}

func bindTo(ctx context.Context, p session.Principal, orgID, projectID uuid.UUID) context.Context {
	return tenant.Into(ctx, tenant.Context{OrgID: orgID, ProjectID: projectID, UserID: p.UserID})
}
