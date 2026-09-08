package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web"
)

// projectScope is the org and project a project-scoped page acts on, with a request already carrying both scopes.
type projectScope struct {
	principal session.Principal
	sid       string
	org       *org.Org
	project   *project.Project
	req       *http.Request
}

// requireProject resolves the org and project the path names, gating membership before any row is read.
func (d Deps) requireProject(w http.ResponseWriter, r *http.Request) (projectScope, bool) {
	return d.requireProjectSlugs(w, r, r.PathValue("org"), r.PathValue("project"))
}

// requireProjectSlugs resolves an explicitly supplied org and project pair, for the Google routes whose path cannot carry one.
func (d Deps) requireProjectSlugs(w http.ResponseWriter, r *http.Request, orgSlug, projectSlug string) (projectScope, bool) {
	p, sid, ok := d.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(d.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return projectScope{}, false
	}
	o, r, ok := d.OrgScopeFor(w, r, p, orgSlug)
	if !ok {
		return projectScope{}, false
	}
	proj, r, ok := d.ProjectScopeFor(w, r, o.ID, projectSlug)
	if !ok {
		return projectScope{}, false
	}
	return projectScope{principal: p, sid: sid, org: o, project: proj, req: r}, true
}

// remember stores the org and project as the session's last-used pair, which only / reads.
func (d Deps) remember(sc projectScope) {
	if sc.principal.ActiveOrgID == sc.org.ID && sc.principal.ActiveProjectID == sc.project.ID {
		return
	}
	updated := sc.principal
	updated.ActiveOrgID = sc.org.ID
	updated.ActiveProjectID = sc.project.ID
	if err := d.UpdateSession(sc.req, sc.sid, updated); err != nil {
		d.LogErr("web: update session", err)
	}
}

// fragment builds the LayoutData an HTMX partial renders with, pinning the org and project so LayoutData.ProjectPath resolves.
func (d Deps) fragment(sc projectScope) web.LayoutData {
	base := d.Base(sc.req, "")
	base.ActiveOrg = &web.ActiveOrg{ID: sc.org.ID.String(), Slug: sc.org.Slug, Name: sc.org.Name}
	base.ActiveProject = &web.ActiveProject{ID: sc.project.ID.String(), Slug: sc.project.Slug, Name: sc.project.Name}
	return base
}

// pathID parses the {id} path value, rendering a 400 page when it is malformed.
func (d Deps) pathID(w http.ResponseWriter, r *http.Request, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		d.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed "+what+" id.")
		return uuid.Nil, false
	}
	return id, true
}
