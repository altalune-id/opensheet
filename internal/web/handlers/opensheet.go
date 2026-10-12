package handlers

import (
	"net/http"

	"github.com/google/uuid"
)

func (d Deps) remember(sc ProjectScope) {
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

func (d Deps) pathID(w http.ResponseWriter, r *http.Request, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		d.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed "+what+" id.")
		return uuid.Nil, false
	}
	return id, true
}

func (d Deps) requireProjectSlugs(w http.ResponseWriter, r *http.Request, orgSlug, projectSlug string) (ProjectScope, bool) {
	r = r.Clone(r.Context())
	r.SetPathValue("org", orgSlug)
	r.SetPathValue("project", projectSlug)
	return d.RequireProject(w, r)
}
