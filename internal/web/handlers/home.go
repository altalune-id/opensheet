package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// HomeHandler serves the root redirect and each org's dashboard.
type HomeHandler struct{ Deps }

// NewHomeHandler wires the handler.
func NewHomeHandler(d Deps, orgs *org.Service, projects *project.Service) *HomeHandler {
	d.Orgs = orgs
	d.Projects = projects
	return &HomeHandler{Deps: d}
}

// Register wires the root redirect and the per-org overview onto mux.
func (h *HomeHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.GetRoot)
	mux.HandleFunc("GET /orgs/{org}", h.GetOverview)
}

// GetRoot sends the bare root to the session's last-used org, the only thing that hint is still for.
func (h *HomeHandler) GetRoot(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.LoadSession(r)
	if !ok || p.UserID == uuid.Nil {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, h.landingPath(r, p)), http.StatusSeeOther) //nolint:gosec // G710: the slug comes from the store and passed org.validateSlug on creation
}

// landingPath prefers the last-used org, then any org the user belongs to, then the create form.
func (h *HomeHandler) landingPath(r *http.Request, p session.Principal) string {
	if h.Orgs == nil {
		return "/orgs"
	}
	orgs, err := h.Orgs.List(r.Context(), p.UserID)
	if err != nil {
		h.LogErr("home: list orgs", err)
		return "/orgs"
	}
	if len(orgs) == 0 {
		return "/orgs/new"
	}
	for _, o := range orgs {
		if o.ID == p.ActiveOrgID {
			return "/orgs/" + o.Slug
		}
	}
	return "/orgs/" + orgs[0].Slug
}

// GetOverview renders /orgs/{org} — that org's dashboard.
func (h *HomeHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	p, sid, ok := h.LoadSession(r)
	if !ok || p.UserID == uuid.Nil {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	o, r, ok := h.OrgScopeFor(w, r, p, r.PathValue("org"))
	if !ok {
		return
	}
	h.remember(r, sid, p, o.ID)

	view := templates.DashboardView{
		OrgSlug:      o.Slug,
		OrgName:      o.Name,
		ActiveProjID: p.ActiveProjectID.String(),
	}
	if orgs, err := h.Orgs.List(r.Context(), p.UserID); err == nil {
		for _, x := range orgs {
			view.Orgs = append(view.Orgs, templates.OrgSummary{ID: x.ID.String(), Slug: x.Slug, Name: x.Name, System: x.System})
		}
	}
	if h.Projects != nil {
		projects, err := h.Projects.List(r.Context(), o.ID)
		if err != nil {
			h.LogErr("home: list projects", err)
		}
		for _, pr := range projects {
			view.Projects = append(view.Projects, templates.ProjectSummary{ID: pr.ID.String(), Slug: pr.Slug, Name: pr.Name, System: pr.System})
		}
	}
	Render(w, r, templates.DashboardLayout(h.LayoutForOrg(r, "Overview · "+o.Name, o.Slug, "overview"), view))
}

// remember records the org the path named as the session's last-used one, which only GetRoot reads.
func (h *HomeHandler) remember(r *http.Request, sid string, p session.Principal, orgID uuid.UUID) {
	if p.ActiveOrgID == orgID {
		return
	}
	updated := p
	updated.ActiveOrgID = orgID
	updated.ActiveProjectID = uuid.Nil
	if err := h.UpdateSession(r, sid, updated); err != nil {
		h.LogErr("home: update session", err)
	}
}
