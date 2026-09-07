package handlers

import (
	"net/http"
	"strings"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// ProjectHandler owns the /projects routes.
type ProjectHandler struct{ Deps }

// NewProjectHandler wires the handler.
func NewProjectHandler(d Deps, projects *project.Service) *ProjectHandler {
	d.Projects = projects
	return &ProjectHandler{Deps: d}
}

// requireOrg resolves the org named by the path, gating membership before anything reads its rows.
func (h *ProjectHandler) requireOrg(w http.ResponseWriter, r *http.Request) (*org.Org, *http.Request, bool) {
	p, _, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return nil, nil, false
	}
	return h.OrgScopeFor(w, r, p, r.PathValue("org"))
}

// GetList renders /orgs/{org}/projects.
func (h *ProjectHandler) GetList(w http.ResponseWriter, r *http.Request) {
	o, r, ok := h.requireOrg(w, r)
	if !ok {
		return
	}
	items, err := h.Projects.List(r.Context(), o.ID)
	if err != nil {
		h.LogErr("web project: list", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load projects.", err)
		return
	}
	Render(w, r, templates.ProjectsLayout(
		h.LayoutForOrg(r, "Projects", o.Slug, "projects"),
		templates.ProjectsView{OrgSlug: o.Slug, Projects: projectSummaries(items)},
	))
}

// GetNew renders /orgs/{org}/projects/new.
func (h *ProjectHandler) GetNew(w http.ResponseWriter, r *http.Request) {
	o, _, ok := h.requireOrg(w, r)
	if !ok {
		return
	}
	Render(w, r, templates.ProjectNewLayout(
		h.LayoutForOrg(r, "Create project", o.Slug, "projects"),
		templates.ProjectNewView{OrgSlug: o.Slug},
	))
}

// PostCreate handles POST /orgs/{org}/projects.
func (h *ProjectHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	p, sid, authed := h.LoadSession(r)
	if !authed {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	o, r, ok := h.OrgScopeFor(w, r, p, r.PathValue("org"))
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	slug := strings.TrimSpace(r.PostForm.Get("slug"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	created, err := h.Projects.Create(r.Context(), o.ID, slug, name)
	if err != nil {
		h.LogErr("web project: create", err)
		msg := err.Error()
		if project.IsAlreadyExistsError(err) {
			msg = "Slug is already taken."
		}
		Render(w, r, templates.ProjectNewLayout(
			h.LayoutForOrg(r, "Create project", o.Slug, "projects"),
			templates.ProjectNewView{OrgSlug: o.Slug, Slug: slug, Name: name, Error: msg, ErrorCode: ErrorRef(err)},
		))
		return
	}
	updated := p
	updated.ActiveOrgID = o.ID
	updated.ActiveProjectID = created.ID
	if err := h.UpdateSession(r, sid, updated); err != nil {
		h.LogErr("web project: update session", err)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, projectPath(o.Slug, created.Slug, "/overview")), http.StatusSeeOther) //nolint:gosec // G710: both slugs are validated by their own slug patterns
}

// PostRename handles POST /orgs/{org}/projects/{project}/rename.
func (h *ProjectHandler) PostRename(w http.ResponseWriter, r *http.Request) {
	o, r, ok := h.requireOrg(w, r)
	if !ok {
		return
	}
	proj, r, ok := h.ProjectScopeFor(w, r, o.ID, r.PathValue("project"))
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if _, err := h.Projects.Rename(r.Context(), proj.ID, name); err != nil {
		h.LogErr("web project: rename", err)
		if project.IsSystemProtectedError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Rename not allowed", "This project is system-protected.", err)
			return
		}
		h.ErrorPage(w, r, http.StatusBadRequest, "Rename failed", err.Error())
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/projects"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// projectPath builds a path under an org's project, so the shape lives in one place.
func projectPath(orgSlug, projectSlug, suffix string) string {
	return "/orgs/" + orgSlug + "/projects/" + projectSlug + suffix
}

func projectSummaries(items []*project.Project) []templates.ProjectSummary {
	out := make([]templates.ProjectSummary, 0, len(items))
	for _, p := range items {
		out = append(out, templates.ProjectSummary{ID: p.ID.String(), Slug: p.Slug, Name: p.Name, System: p.System})
	}
	return out
}

// Register wires the project routes onto mux.
func (h *ProjectHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs/{org}/projects", h.GetList)
	mux.HandleFunc("GET /orgs/{org}/projects/new", h.GetNew)
	mux.HandleFunc("POST /orgs/{org}/projects", h.PostCreate)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/rename", h.PostRename)
}
