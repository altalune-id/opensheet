package data

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

// Orgs resolves an org slug before any tenant scope exists.
type Orgs interface {
	BySlug(ctx context.Context, slug string) (OrgRef, error)
}

// Projects resolves a project slug inside an org.
type Projects interface {
	BySlug(ctx context.Context, orgID uuid.UUID, slug string) (ProjectRef, error)
}

// Sheets resolves a sheet slug inside a project.
type Sheets interface {
	BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error)
}

// OrgRef is the org the data plane needs, referenced across the module boundary by id.
type OrgRef struct{ ID uuid.UUID }

// ProjectRef is the project the data plane needs, referenced across the module boundary by id.
type ProjectRef struct{ ID uuid.UUID }

type scope struct {
	orgID     uuid.UUID
	projectID uuid.UUID
	sheet     *sheet.Sheet
}

type projectScope struct {
	orgID     uuid.UUID
	projectID uuid.UUID
}

type resolver struct {
	orgs     Orgs
	projects Projects
	sheets   Sheets
}

// SECURITY: scope comes from the path — this surface has no session and no membership to gate on.
func (rs resolver) resolve(ctx context.Context, orgSlug, projectSlug, sheetSlug string) (context.Context, scope, error) {
	ctx, ps, err := rs.resolveProject(ctx, orgSlug, projectSlug)
	if err != nil {
		return ctx, scope{}, err
	}
	sh, err := rs.sheets.BySlug(ctx, ps.orgID, ps.projectID, sheetSlug)
	if err != nil {
		return ctx, scope{}, maskNotFound(err)
	}
	return ctx, scope{orgID: ps.orgID, projectID: ps.projectID, sheet: sh}, nil
}

// SECURITY: the same path-derived scope and the same masking, minus the sheet — the tabs routes name a spreadsheet id, not a slug.
func (rs resolver) resolveProject(ctx context.Context, orgSlug, projectSlug string) (context.Context, projectScope, error) {
	o, err := rs.orgs.BySlug(ctx, orgSlug)
	if err != nil {
		return ctx, projectScope{}, maskNotFound(err)
	}
	ctx = tenant.Into(ctx, tenant.Context{OrgID: o.ID})

	p, err := rs.projects.BySlug(ctx, o.ID, projectSlug)
	if err != nil {
		return ctx, projectScope{}, maskNotFound(err)
	}
	ctx = tenant.WithProject(ctx, p.ID)
	return ctx, projectScope{orgID: o.ID, projectID: p.ID}, nil
}
