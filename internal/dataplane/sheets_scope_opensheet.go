package dataplane

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

// Sheets resolves a sheet slug inside a project.
type Sheets interface {
	BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error)
}

type sheetScope struct {
	orgID     uuid.UUID
	projectID uuid.UUID
	sheet     *sheet.Sheet
}

type sheetProjectScope struct {
	orgID     uuid.UUID
	projectID uuid.UUID
}

type sheetResolver struct {
	orgs     Orgs
	projects Projects
	sheets   Sheets
}

// SECURITY: scope comes from the path — this surface has no session and no membership to gate on. NOTE: not the shared resolver, which masks every lookup failure; here an unexpected failure must stay a 500.
func (rs sheetResolver) resolve(ctx context.Context, orgSlug, projectSlug, sheetSlug string) (context.Context, sheetScope, error) {
	ctx, ps, err := rs.resolveProject(ctx, orgSlug, projectSlug)
	if err != nil {
		return ctx, sheetScope{}, err
	}
	sh, err := rs.sheets.BySlug(ctx, ps.orgID, ps.projectID, sheetSlug)
	if err != nil {
		return ctx, sheetScope{}, maskNotFound(err)
	}
	return ctx, sheetScope{orgID: ps.orgID, projectID: ps.projectID, sheet: sh}, nil
}

// SECURITY: the same path-derived scope and the same masking, minus the sheet — the tabs routes name a spreadsheet id, not a slug.
func (rs sheetResolver) resolveProject(ctx context.Context, orgSlug, projectSlug string) (context.Context, sheetProjectScope, error) {
	o, err := rs.orgs.BySlug(ctx, orgSlug)
	if err != nil {
		return ctx, sheetProjectScope{}, maskNotFound(err)
	}
	ctx = tenant.Into(ctx, tenant.Context{OrgID: o.ID})

	p, err := rs.projects.BySlug(ctx, o.ID, projectSlug)
	if err != nil {
		return ctx, sheetProjectScope{}, maskNotFound(err)
	}
	ctx = tenant.WithProject(ctx, p.ID)
	return ctx, sheetProjectScope{orgID: o.ID, projectID: p.ID}, nil
}
