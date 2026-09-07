package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// OrgHandler owns /orgs, /orgs/new, /orgs/{slug} and members routes.
type OrgHandler struct{ Deps }

// NewOrgHandler wires the handler.
func NewOrgHandler(d Deps, orgs *org.Service) *OrgHandler {
	d.Orgs = orgs
	return &OrgHandler{Deps: d}
}

// GetList renders /orgs.
func (h *OrgHandler) GetList(w http.ResponseWriter, r *http.Request) {
	p, authed := h.requireAuth(w, r)
	if !authed {
		return
	}
	items, err := h.Orgs.List(r.Context(), p.UserID)
	if err != nil {
		h.LogErr("web org: list", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load orgs.", err)
		return
	}
	Render(w, r, templates.OrgsLayout(h.Layout(r, "Organizations", web.ActiveNav{Scope: web.NavScopeOrg}), templates.OrgsView{Orgs: orgSummaries(items)}))
}

// GetNew renders /orgs/new.
func (h *OrgHandler) GetNew(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAuth(w, r); !ok {
		return
	}
	if !h.Caps.OrgCreation {
		h.ErrorPage(w, r, http.StatusForbidden, "Not allowed", "Organization creation is disabled in this deployment.")
		return
	}
	Render(w, r, templates.OrgNewLayout(h.Layout(r, "Create organization", web.ActiveNav{Scope: web.NavScopeOrg}), templates.OrgNewView{}))
}

// PostCreate handles POST /orgs (org creation is capability-gated).
func (h *OrgHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	p, sid, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if !h.Caps.OrgCreation {
		h.ErrorPage(w, r, http.StatusForbidden, "Not allowed", "Organization creation is disabled in this deployment.")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	slug := strings.TrimSpace(r.PostForm.Get("slug"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	created, err := h.Orgs.Create(r.Context(), org.CreateRequest{Slug: slug, Name: name, OwnerID: p.UserID})
	if err != nil {
		h.LogErr("web org: create", err)
		h.renderNewErr(w, r, slug, name, err)
		return
	}
	updated := p
	updated.ActiveOrgID = created.ID
	updated.ActiveProjectID = uuid.Nil
	if err := h.UpdateSession(r, sid, updated); err != nil {
		h.LogErr("web org: update session", err)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/orgs/"+created.Slug+"/projects"), http.StatusSeeOther)
}

// PostRename handles POST /orgs/{slug}/rename.
func (h *OrgHandler) PostRename(w http.ResponseWriter, r *http.Request) {
	p, authed := h.requireAuth(w, r)
	if !authed {
		return
	}
	slug := r.PathValue("org")
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	o, r, ok := h.OrgScopeFor(w, r, p, slug)
	if !ok {
		return
	}
	// SECURITY: the slug comes from the URL, so membership in that org must be checked here — RLS no longer narrows this to the active org.
	canManage, mErr := h.isManager(r.Context(), o.ID, p.UserID)
	if mErr != nil || !canManage {
		h.ErrorPage(w, r, http.StatusForbidden, "Not allowed", "You must be an admin or owner to rename this organization.")
		return
	}
	if _, err := h.Orgs.Rename(r.Context(), o.ID, name); err != nil {
		h.LogErr("web org: rename", err)
		if org.IsSystemProtectedError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Rename not allowed", "This organization is system-protected.", err)
			return
		}
		h.ErrorPage(w, r, http.StatusBadRequest, "Rename failed", err.Error())
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+slug+"/members"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// GetShow renders /orgs/{slug} — the members view.
func (h *OrgHandler) GetShow(w http.ResponseWriter, r *http.Request) {
	p, authed := h.requireAuth(w, r)
	if !authed {
		return
	}
	slug := r.PathValue("org")
	o, r, ok := h.OrgScopeFor(w, r, p, slug)
	if !ok {
		return
	}
	profiles, err := h.Orgs.ListMemberProfiles(r.Context(), o.ID)
	if err != nil {
		h.LogErr("web org: members", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "Members failed", "Could not load members.", err)
		return
	}
	canManage, _ := h.isManager(r.Context(), o.ID, p.UserID)
	Render(w, r, templates.MembersLayout(h.LayoutForOrg(r, o.Name, slug, "members"), templates.MembersView{
		OrgSlug:   slug,
		Members:   memberProfileRows(profiles, o.ID, p.UserID),
		CanManage: canManage,
	}))
}

// PostRemoveMember handles POST /orgs/{slug}/members/{user}/remove.
func (h *OrgHandler) PostRemoveMember(w http.ResponseWriter, r *http.Request) {
	p, authed := h.requireAuth(w, r)
	if !authed {
		return
	}
	slug := r.PathValue("org")
	o, r, ok := h.OrgScopeFor(w, r, p, slug)
	if !ok {
		return
	}
	canManage, mErr := h.isManager(r.Context(), o.ID, p.UserID)
	if mErr != nil || !canManage {
		h.ErrorPage(w, r, http.StatusForbidden, "Not allowed", "You must be an admin or owner to manage members.")
		return
	}
	userID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed user id.")
		return
	}
	if err := h.Orgs.RemoveMember(r.Context(), o.ID, userID); err != nil {
		h.LogErr("web org: remove member", err)
		if org.IsSystemProtectedError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Remove not allowed", "This membership is system-protected.", err)
			return
		}
		if org.IsMembershipMissingError(err) || org.IsNotFoundError(err) {
			h.ErrorPage(w, r, http.StatusNotFound, "Member not found", "That person is not a member of this organization.")
			return
		}
		if org.IsSelfRemovalError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Remove not allowed", "You cannot remove your own membership.")
			return
		}
		if org.IsOwnerRemovalError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Remove not allowed", "An owner cannot be removed.")
			return
		}
		// SECURITY: err.Error() names internal ids, so it stays in the log and never reaches the page.
		h.ErrorPage(w, r, http.StatusInternalServerError, "Remove failed", "Could not remove that member.")
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+slug+"/members"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// SECURITY: ctx must already carry the tenant scope; MembershipOf is RLS-filtered by org.
func (h *OrgHandler) isManager(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	m, err := h.Orgs.MembershipOf(ctx, orgID, userID)
	if err != nil {
		return false, err
	}
	return m.Role == org.RoleOwner || m.Role == org.RoleAdmin, nil
}

func (h *OrgHandler) requireAuth(w http.ResponseWriter, r *http.Request) (session.Principal, bool) {
	p, _, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return session.Principal{}, false
	}
	return p, true
}

func (h *OrgHandler) renderNewErr(w http.ResponseWriter, r *http.Request, slug, name string, err error) {
	msg := err.Error()
	switch {
	case org.IsAlreadyExistsError(err):
		msg = "Slug is already taken."
	case org.IsCreationDisabledError(err):
		msg = "Organization creation is disabled."
	}
	Render(w, r, templates.OrgNewLayout(
		h.Layout(r, "Create organization", web.ActiveNav{Scope: web.NavScopeOrg}),
		templates.OrgNewView{Slug: slug, Name: name, Error: msg, ErrorCode: ErrorRef(err)},
	))
}

func orgSummaries(items []*org.Org) []templates.OrgSummary {
	out := make([]templates.OrgSummary, 0, len(items))
	for _, o := range items {
		out = append(out, templates.OrgSummary{ID: o.ID.String(), Slug: o.Slug, Name: o.Name, System: o.System})
	}
	return out
}

func memberProfileRows(items []*org.MemberProfile, orgID, viewer uuid.UUID) []templates.MemberRow {
	// NOTE: the viewer is always a member of the org OrgScopeFor resolved, so their role is in this same list.
	var viewerRole org.Role
	for _, m := range items {
		if m.UserID == viewer {
			viewerRole = m.Role
			break
		}
	}
	out := make([]templates.MemberRow, 0, len(items))
	for _, m := range items {
		out = append(out, templates.MemberRow{
			UserID:    m.UserID.String(),
			Email:     m.Email,
			Name:      m.Name,
			Role:      string(m.Role),
			System:    m.System,
			IsSelf:    m.UserID == viewer,
			Removable: org.RemovalRefusal(orgID, viewer, m.UserID, viewerRole, m.Role, m.System) == nil,
		})
	}
	return out
}

// Register wires all org routes onto the mux.
func (h *OrgHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs", h.GetList)
	mux.HandleFunc("GET /orgs/new", h.GetNew)
	mux.HandleFunc("POST /orgs", h.PostCreate)
	mux.HandleFunc("GET /orgs/{org}/members", h.GetShow)
	mux.HandleFunc("POST /orgs/{org}/rename", h.PostRename)
	mux.HandleFunc("POST /orgs/{org}/members/{user}/remove", h.PostRemoveMember)
}
