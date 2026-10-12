package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
)

// OrgScope is the org an org-scoped route acts on, with a request already carrying the tenant scope.
type OrgScope struct {
	principal session.Principal
	sid       string
	org       *org.Org
	req       *http.Request
}

// ProjectScope is the org and project a project-scoped route acts on, with a request already carrying both scopes.
type ProjectScope struct {
	principal session.Principal
	sid       string
	org       *org.Org
	project   *project.Project
	req       *http.Request
}

// RequireOrg resolves the org the path names, gating membership before anything reads its rows. SECURITY: the slug is attacker-supplied and RLS cannot gate it, so this membership check is the only thing separating one org's members from another's rows.
func (d Deps) RequireOrg(w http.ResponseWriter, r *http.Request) (OrgScope, bool) {
	return d.RequireOrgFrom(w, r, func(r *http.Request) string { return r.PathValue("org") })
}

// RequireOrgFrom is RequireOrg for a route that names its org elsewhere, such as a form field; slug is read only once the caller is signed in.
func (d Deps) RequireOrgFrom(w http.ResponseWriter, r *http.Request, slug func(*http.Request) string) (OrgScope, bool) {
	p, sid, ok := d.RequireSignedIn(w, r)
	if !ok {
		return OrgScope{}, false
	}
	o, r, ok := d.OrgScopeFor(w, r, p, slug(r))
	if !ok {
		return OrgScope{}, false
	}
	return OrgScope{principal: p, sid: sid, org: o, req: r}, true
}

// RequireSignedIn loads the caller's session, redirecting to login when there is none.
func (d Deps) RequireSignedIn(w http.ResponseWriter, r *http.Request) (session.Principal, string, bool) {
	p, sid, ok := d.LoadSession(r)
	if !ok || p.UserID == uuid.Nil {
		http.Redirect(w, r, ResolveReturnTo(d.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return session.Principal{}, "", false
	}
	return p, sid, true
}

// MemberOrgScopes returns an OrgScope for every org the caller belongs to. SECURITY: org.Service.List joins the caller's memberships, so every org here is one the caller is a member of right now.
func (d Deps) MemberOrgScopes(r *http.Request, p session.Principal, sid string) ([]OrgScope, error) {
	orgs, err := d.Orgs.List(r.Context(), p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]OrgScope, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, OrgScope{principal: p, sid: sid, org: o, req: orgScopedRequest(r, p, o)})
	}
	return out, nil
}

func orgScopedRequest(r *http.Request, p session.Principal, o *org.Org) *http.Request {
	return r.WithContext(tenant.Into(r.Context(), tenant.Context{OrgID: o.ID, UserID: p.UserID}))
}

// RequireProject resolves the org and project the path names, gating org membership before the project is looked up. SECURITY: membership is org-level by design, so this admits ANY project in an org the caller belongs to — see ../../../docs/multitenancy/request-scope.md.
func (d Deps) RequireProject(w http.ResponseWriter, r *http.Request) (ProjectScope, bool) {
	sc, ok := d.RequireOrg(w, r)
	if !ok {
		return ProjectScope{}, false
	}
	proj, req, ok := d.ProjectScopeFor(w, sc.req, sc.org.ID, sc.req.PathValue("project"))
	if !ok {
		return ProjectScope{}, false
	}
	return ProjectScope{principal: sc.principal, sid: sc.sid, org: sc.org, project: proj, req: req}, true
}
