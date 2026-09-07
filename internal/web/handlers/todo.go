package handlers

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/todo"
	"altalune.id/template/internal/web/templates"
)

// TodoHandler wraps the projects/todos services for the org-scoped todo routes.
type TodoHandler struct {
	Deps
	Todos *todo.Service
}

// NewTodoHandler wires the handler.
func NewTodoHandler(d Deps, projects *project.Service, todos *todo.Service) *TodoHandler {
	d.Projects = projects
	return &TodoHandler{Deps: d, Todos: todos}
}

// projectScope is the org and project a todo route acts on, with a request already carrying both scopes.
type projectScope struct {
	principal session.Principal
	sid       string
	org       *org.Org
	project   *project.Project
	req       *http.Request
}

// requireProject resolves the org and project the path names, gating membership before any row is read.
func (h *TodoHandler) requireProject(w http.ResponseWriter, r *http.Request) (projectScope, bool) {
	p, sid, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return projectScope{}, false
	}
	o, r, ok := h.OrgScopeFor(w, r, p, r.PathValue("org"))
	if !ok {
		return projectScope{}, false
	}
	proj, r, ok := h.ProjectScopeFor(w, r, o.ID, r.PathValue("project"))
	if !ok {
		return projectScope{}, false
	}
	return projectScope{principal: p, sid: sid, org: o, project: proj, req: r.WithContext(r.Context())}, true
}

// remember stores the org and project as the session's last-used pair, which only /  reads.
func (h *TodoHandler) remember(sc projectScope) {
	if sc.principal.ActiveOrgID == sc.org.ID && sc.principal.ActiveProjectID == sc.project.ID {
		return
	}
	updated := sc.principal
	updated.ActiveOrgID = sc.org.ID
	updated.ActiveProjectID = sc.project.ID
	if err := h.UpdateSession(sc.req, sc.sid, updated); err != nil {
		h.LogErr("web todo: update session", err)
	}
}

// GetOverview renders the project overview page.
func (h *TodoHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	items, err := h.Todos.List(sc.req.Context(), todo.ListOpts{})
	if err != nil {
		h.LogErr("web overview: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Load failed", "Could not load project overview.", err)
		return
	}
	var open, done int
	for _, t := range items {
		if t.Done {
			done++
		} else {
			open++
		}
	}
	Render(w, sc.req, templates.OverviewLayout(
		h.LayoutForProject(sc.req, "Overview · "+sc.project.Name, sc.org.Slug, sc.project, "overview"),
		templates.OverviewView{
			OrgSlug:     sc.org.Slug,
			ProjectID:   sc.project.ID.String(),
			ProjectSlug: sc.project.Slug,
			ProjectName: sc.project.Name,
			TotalTodos:  len(items),
			OpenTodos:   open,
			DoneTodos:   done,
		},
	))
}

// GetTodos renders the full page.
func (h *TodoHandler) GetTodos(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	items, err := h.Todos.List(sc.req.Context(), todo.ListOpts{})
	if err != nil {
		h.LogErr("web todo: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load todos.", err)
		return
	}
	Render(w, sc.req, templates.TodosLayout(
		h.LayoutForProject(sc.req, "Todos · "+sc.project.Name, sc.org.Slug, sc.project, "todos"),
		templates.TodosView{
			OrgSlug:     sc.org.Slug,
			ProjectID:   sc.project.ID.String(),
			ProjectSlug: sc.project.Slug,
			ProjectName: sc.project.Name,
			Items:       renderRowsFor(sc.org.Slug, sc.project.Slug, items),
		},
	))
}

// PostCreate creates a todo and returns the refreshed list fragment.
func (h *TodoHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	if _, err := h.Todos.Create(sc.req.Context(), strings.TrimSpace(r.PostForm.Get("title"))); err != nil {
		h.LogErr("web todo: create", err)
	}
	h.writeListFragment(w, sc)
}

// PostToggle flips done and returns the single-row partial.
func (h *TodoHandler) PostToggle(w http.ResponseWriter, r *http.Request) {
	sc, t, ok := h.requireTodo(w, r)
	if !ok {
		return
	}
	updated, err := h.Todos.Toggle(sc.req.Context(), t.ID)
	if err != nil {
		if todo.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That todo no longer exists.")
			return
		}
		h.LogErr("web todo: toggle", err)
		// SECURITY: err.Error() names internal ids, so the page shows the code and request id instead.
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Toggle failed", "Could not update that todo.", err)
		return
	}
	Render(w, sc.req, templates.TodoRowFragment(h.Base(sc.req, ""), todoRow(sc.org.Slug, sc.project.Slug, updated)))
}

// Delete removes the row.
func (h *TodoHandler) Delete(w http.ResponseWriter, r *http.Request) {
	sc, t, ok := h.requireTodo(w, r)
	if !ok {
		return
	}
	if err := h.Todos.Delete(sc.req.Context(), t.ID); err != nil && !todo.IsNotFoundError(err) {
		h.LogErr("web todo: delete", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

// PostClear removes all done todos and returns the refreshed list fragment.
func (h *TodoHandler) PostClear(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	if _, err := h.Todos.ClearDone(sc.req.Context()); err != nil {
		h.LogErr("web todo: clear", err)
	}
	h.writeListFragment(w, sc)
}

// requireTodo resolves the project scope from the path, then the todo inside it.
func (h *TodoHandler) requireTodo(w http.ResponseWriter, r *http.Request) (projectScope, *todo.Todo, bool) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return projectScope{}, nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed todo id.")
		return projectScope{}, nil, false
	}
	t, err := h.Todos.ByID(sc.req.Context(), id)
	if err != nil {
		if todo.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That todo no longer exists.")
			return projectScope{}, nil, false
		}
		h.LogErr("web todo: byID", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that todo.", err)
		return projectScope{}, nil, false
	}
	return sc, t, true
}

func (h *TodoHandler) writeListFragment(w http.ResponseWriter, sc projectScope) {
	items, err := h.Todos.List(sc.req.Context(), todo.ListOpts{})
	if err != nil {
		h.LogErr("web todo: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load todos.", err)
		return
	}
	Render(w, sc.req, templates.TodoList(h.Base(sc.req, ""), renderRowsFor(sc.org.Slug, sc.project.Slug, items)))
}

// Register wires the todo routes onto mux.
func (h *TodoHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/overview", h.GetOverview)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/todos", h.GetTodos)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/todos", h.PostCreate)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/todos/clear", h.PostClear)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/todos/{id}/toggle", h.PostToggle)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/todos/{id}/delete", h.Delete)
	mux.HandleFunc("DELETE /orgs/{org}/projects/{project}/todos/{id}", h.Delete)
}

func todoRow(orgSlug, projectSlug string, t *todo.Todo) templates.TodoRow {
	return templates.TodoRow{
		ID:          t.ID.String(),
		Title:       t.Title,
		Done:        t.Done,
		OrgSlug:     orgSlug,
		ProjectSlug: projectSlug,
	}
}

func renderRowsFor(orgSlug, projectSlug string, items []*todo.Todo) []templates.TodoRow {
	rows := make([]templates.TodoRow, 0, len(items))
	for _, t := range items {
		rows = append(rows, todoRow(orgSlug, projectSlug, t))
	}
	return rows
}
