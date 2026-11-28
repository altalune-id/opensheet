package handlers

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	rolepkg "altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
	slugs "altalune.id/opensheet/slug"
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
	Render(w, r, templates.OrgNewLayout(h.Layout(r, "Create organization", web.ActiveNav{Scope: web.NavScopeOrg}), templates.OrgNewView{Slug: slugs.Generate()}))
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
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	if !holds(r, authz.MembersWrite) {
		h.refuseRole(w, r, "org.rename_managers_only")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if _, err := h.Orgs.Rename(r.Context(), o.ID, name); err != nil {
		h.LogErr("web org: rename", err)
		if authz.IsDeniedError(err) {
			h.refuseRole(w, r, "org.rename_managers_only", err)
			return
		}
		if org.IsSystemProtectedError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Rename not allowed", "This organization is system-protected.", err)
			return
		}
		h.ErrorPage(w, r, http.StatusBadRequest, "Rename failed", err.Error())
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/members"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// GetShow renders /orgs/{slug} — the members view.
func (h *OrgHandler) GetShow(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	h.renderMembers(w, sc, http.StatusOK, nil)
}

type grantEdit struct {
	userID uuid.UUID
	grant  org.ProjectGrant
	err    error
	msgKey string
}

func (h *OrgHandler) renderMembers(w http.ResponseWriter, sc OrgScope, status int, edit *grantEdit) {
	p, o, r := sc.principal, sc.org, sc.req
	profiles, err := h.Orgs.ListMemberProfiles(r.Context(), o.ID)
	if authz.IsDeniedError(err) {
		Render(w, r, templates.MembersLayout(h.LayoutForOrg(r, o.Name, o.Slug, "members"), templates.MembersView{OrgSlug: o.Slug, OrgName: o.Name, CannotView: true}))
		return
	}
	if err != nil {
		h.LogErr("web org: members", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "Members failed", "Could not load members.", err)
		return
	}
	held := authz.Held(r.Context())
	role := viewerRole(profiles, p.UserID)
	canManage := authz.CanOrgWide(r.Context(), authz.MembersWrite) == nil
	rowHeld := held
	if !canManage {
		rowHeld = nil
	}
	var custom []*rolepkg.Role
	if canManage {
		custom = h.customRoles(r)
	}
	var options []templates.GrantOption
	if canManage {
		if options, err = h.grantOptions(r, o.ID); err != nil {
			h.LogErr("web org: member grant projects", err)
			h.ErrorPage(w, r, http.StatusInternalServerError, "Members failed", "Could not load members.", err)
			return
		}
	}
	perms := h.memberPermissions(r, profiles)
	rows := memberProfileRows(profiles, o.ID, p.UserID, rowHeld, perms, custom)
	if canManage {
		attachGrants(rows, profiles, held, perms, options)
	}
	view := templates.MembersView{
		OrgSlug:        o.Slug,
		OrgName:        o.Name,
		Members:        rows,
		CanManage:      canManage,
		GrantableRoles: grantableRoles(held, role, custom),
		ViewerIsOwner:  role == org.RoleOwner,
		RoleUpdated:    r.URL.Query().Has("role_updated"),
		AccessUpdated:  r.URL.Query().Has("access_updated"),
	}
	if edit != nil {
		view.Error = i18n.TranslatorFrom(r.Context()).T(edit.msgKey)
		view.ErrorCode = ErrorRef(edit.err)
		for i := range rows {
			if rows[i].Grant != nil && rows[i].UserID == edit.userID.String() {
				g := grantView(edit.grant.All, edit.grant.ProjectIDs, options)
				rows[i].Grant, rows[i].AccessOpen = &g, true
			}
		}
	}
	RenderStatus(w, r, status, templates.MembersLayout(h.LayoutForOrg(r, o.Name, o.Slug, "members"), view))
}

//i18n:use members.access_role_denied

// PostSetAccess handles POST /orgs/{org}/members/{user}/projects.
func (h *OrgHandler) PostSetAccess(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	if authz.CanOrgWide(r.Context(), authz.MembersWrite) != nil {
		h.refuseRole(w, r, "members.read_only")
		return
	}
	tr := i18n.TranslatorFrom(r.Context())
	userID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed user id.")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	g, ok := strictGrant(r)
	if !ok {
		h.ErrorPage(w, r, http.StatusBadRequest, tr.T("members.error.access_title"), tr.T("members.error.access_empty"))
		return
	}
	err = h.Orgs.SetProjectGrant(r.Context(), o.ID, userID, g)
	switch {
	case err == nil:
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/members?access_updated=1"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
	case authz.IsDeniedError(err):
		h.refuseRole(w, r, "members.read_only", err)
	case org.IsProjectGrantEmptyError(err):
		h.renderMembers(w, sc, http.StatusUnprocessableEntity, &grantEdit{userID: userID, grant: g, err: err, msgKey: "members.error.access_empty"})
	case org.IsProjectGrantEscalationError(err):
		h.renderMembers(w, sc, http.StatusForbidden, &grantEdit{userID: userID, grant: g, err: err, msgKey: "members.error.access_escalation"})
	case org.IsProjectGrantConflictError(err):
		h.ErrorPage(w, r, http.StatusConflict, tr.T("members.error.access_title"), tr.T("members.access_owner"), err)
	case authz.IsEscalationError(err):
		h.refuseRole(w, r, "members.access_role_denied", err)
	case org.IsMembershipMissingError(err), org.IsNotFoundError(err):
		h.ErrorPage(w, r, http.StatusNotFound, tr.T("members.error.access_title"), tr.T("members.error.not_found"), err)
	case org.IsSystemProtectedError(err):
		h.ErrorPage(w, r, http.StatusConflict, tr.T("members.error.access_title"), tr.T("members.error.system"), err)
	default:
		h.LogErr("web org: set project grant", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, tr.T("members.error.access_title"), tr.T("members.error.access_failed"), err)
	}
}

// SECURITY: a member edit names its choice, so a stale or forged post without one never widens a restricted member.
func strictGrant(r *http.Request) (org.ProjectGrant, bool) {
	switch r.PostForm.Get("grant") {
	case "all", "selected":
		return memberGrant(r), true
	}
	return org.ProjectGrant{}, false
}

// SECURITY: only "selected" restricts, and every other choice drops the checkbox values a hidden list still submits.
func memberGrant(r *http.Request) org.ProjectGrant {
	if r.PostForm.Get("grant") != "selected" {
		return org.ProjectGrant{All: true}
	}
	return org.ProjectGrant{ProjectIDs: formUUIDs(r.PostForm["project_ids"])}
}

func (d Deps) grantOptions(r *http.Request, orgID uuid.UUID) ([]templates.GrantOption, error) {
	if d.Projects == nil {
		return nil, nil
	}
	items, err := d.Projects.List(r.Context(), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]templates.GrantOption, 0, len(items))
	for _, p := range items {
		out = append(out, templates.GrantOption{ID: p.ID.String(), Name: p.Name})
	}
	return out, nil
}

func grantView(all bool, held []uuid.UUID, options []templates.GrantOption) templates.GrantView {
	g := templates.GrantView{All: all, Options: make([]templates.GrantOption, len(options))}
	for i, o := range options {
		o.Checked = !all && slices.ContainsFunc(held, func(id uuid.UUID) bool { return id.String() == o.ID })
		g.Options[i] = o
	}
	return g
}

// SECURITY: the access form shows only where SetProjectGrant would accept it: not an owner, not a system membership, and a role the viewer covers.
func attachGrants(rows []templates.MemberRow, items []*org.MemberProfile, held []authz.Permission, perms map[uuid.UUID][]authz.Permission, options []templates.GrantOption) {
	for i, m := range items {
		mp, known := perms[m.UserID]
		if !known || m.System || m.Role == org.RoleOwner || !authz.Covers(held, mp...) {
			continue
		}
		g := grantView(m.AllProjects, m.ProjectIDs, options)
		rows[i].Grant = &g
	}
}

// PostRemoveMember handles POST /orgs/{slug}/members/{user}/remove.
func (h *OrgHandler) PostRemoveMember(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	if !holds(r, authz.MembersWrite) {
		h.refuseRole(w, r, "members.read_only")
		return
	}
	userID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed user id.")
		return
	}
	if err := h.Orgs.RemoveMember(r.Context(), o.ID, userID); err != nil {
		h.LogErr("web org: remove member", err)
		if authz.IsDeniedError(err) {
			h.refuseRole(w, r, "members.read_only", err)
			return
		}
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
		if h.refuseRoleChange(w, r, o, "", err) {
			return
		}
		// SECURITY: err.Error() names internal ids, so it stays in the log and never reaches the page.
		h.ErrorPage(w, r, http.StatusInternalServerError, "Remove failed", "Could not remove that member.")
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/members"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

//i18n:use members.error.*
//i18n:use authz.escalation
//i18n:use authz.escalation_remove
//i18n:use authz.last_owner

// PostSetRole handles POST /orgs/{org}/members/{user}/role.
func (h *OrgHandler) PostSetRole(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	if !holds(r, authz.MembersWrite) {
		h.refuseRole(w, r, "members.read_only")
		return
	}
	tr := i18n.TranslatorFrom(r.Context())
	userID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed user id.")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	role, roleID := parseRoleValue(strings.TrimSpace(r.PostForm.Get("role")))
	err = h.Orgs.SetRole(r.Context(), o.ID, userID, role, roleID)
	switch {
	case err == nil:
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/members?role_updated=1"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
	case authz.IsDeniedError(err):
		h.refuseRole(w, r, "members.read_only", err)
	case org.IsInvalidRoleError(err):
		h.ErrorPage(w, r, http.StatusBadRequest, tr.T("members.error.title"), tr.T("members.error.bad_role"), err)
	case org.IsMembershipMissingError(err), org.IsNotFoundError(err):
		h.ErrorPage(w, r, http.StatusNotFound, tr.T("members.error.title"), tr.T("members.error.not_found"), err)
	case org.IsSystemProtectedError(err):
		h.ErrorPage(w, r, http.StatusConflict, tr.T("members.error.title"), tr.T("members.error.system"), err)
	case h.refuseRoleChange(w, r, o, role, err):
	default:
		h.LogErr("web org: set role", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, tr.T("members.error.title"), tr.T("members.error.role_failed"), err)
	}
}

func (h *OrgHandler) refuseRoleChange(w http.ResponseWriter, r *http.Request, o *org.Org, role org.Role, err error) bool {
	tr := i18n.TranslatorFrom(r.Context())
	esc, escalated := errors.AsType[*authz.EscalationError](err)
	switch {
	case escalated && esc.Op == authz.EscalationRemove:
		h.ErrorPage(w, r, http.StatusForbidden, tr.T("errors.not_allowed.title"), tr.T("authz.escalation_remove"), err)
	case escalated:
		h.ErrorPage(w, r, http.StatusForbidden, tr.T("errors.not_allowed.title"), tr.T("authz.escalation", "Role", string(role)), err)
	case authz.IsLastOwnerError(err):
		h.ErrorPage(w, r, http.StatusConflict, tr.T("errors.not_allowed.title"), tr.T("authz.last_owner", "Org", o.Name), err)
	default:
		return false
	}
	return true
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
	if slug == "" {
		slug = slugs.Generate()
	}
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

// NOTE: the viewer is always a member of the org OrgScopeFor resolved, so their role is in this same list.
func viewerRole(items []*org.MemberProfile, viewer uuid.UUID) org.Role {
	for _, m := range items {
		if m.UserID == viewer {
			return m.Role
		}
	}
	return ""
}

func grantableRoles(held []authz.Permission, viewer org.Role, custom []*rolepkg.Role) []templates.RoleOption {
	var out []templates.RoleOption
	for _, r := range []org.Role{org.RoleOwner, org.RoleAdmin, org.RoleMember} {
		perms := authz.Builtin(string(r))
		if org.RoleChangeRefusal(held, viewer, r, r, perms, perms) == nil {
			out = append(out, templates.RoleOption{Value: string(r)})
		}
	}
	for _, c := range custom {
		if org.RoleChangeRefusal(held, viewer, org.RoleCustom, org.RoleCustom, c.Permissions, c.Permissions) == nil {
			out = append(out, templates.RoleOption{Value: c.ID.String(), Label: c.Name})
		}
	}
	return out
}

// SECURITY: a member whose set could not be read is left out, so the page offers no edit or remove for them.
func (h *OrgHandler) memberPermissions(r *http.Request, items []*org.MemberProfile) map[uuid.UUID][]authz.Permission {
	out := make(map[uuid.UUID][]authz.Permission, len(items))
	for _, m := range items {
		perms, err := h.Authz.PermissionsOf(r.Context(), string(m.Role), m.RoleID)
		if err != nil {
			h.LogErr("web org: member permissions", err)
			continue
		}
		out[m.UserID] = perms
	}
	return out
}

func memberProfileRows(items []*org.MemberProfile, orgID, viewer uuid.UUID, held []authz.Permission, perms map[uuid.UUID][]authz.Permission, custom []*rolepkg.Role) []templates.MemberRow {
	viewerRole := viewerRole(items, viewer)
	owners := 0
	for _, m := range items {
		if m.Role == org.RoleOwner {
			owners++
		}
	}
	out := make([]templates.MemberRow, 0, len(items))
	for _, m := range items {
		lastOwner := m.Role == org.RoleOwner && owners == 1
		mp, known := perms[m.UserID]
		out = append(out, templates.MemberRow{
			UserID:       m.UserID.String(),
			Email:        m.Email,
			Name:         m.Name,
			Role:         string(m.Role),
			RoleValue:    roleValue(m.Role, m.RoleID),
			RoleName:     customRoleName(custom, m.RoleID),
			System:       m.System,
			IsSelf:       m.UserID == viewer,
			Removable:    known && org.RemovalRefusal(orgID, viewer, m.UserID, viewerRole, m.Role, m.System, held, mp) == nil,
			RoleEditable: known && !m.System && !lastOwner && org.RoleChangeRefusal(held, viewerRole, m.Role, m.Role, mp, mp) == nil,
			LastOwner:    lastOwner,
			AllProjects:  m.AllProjects,
			ProjectCount: len(m.ProjectIDs),
		})
	}
	return out
}

func (d Deps) customRoles(r *http.Request) []*rolepkg.Role {
	if d.Roles == nil {
		return nil
	}
	items, err := d.Roles.List(r.Context())
	if err != nil {
		d.LogErr("web role: picker", err)
		return nil
	}
	return items
}

func customRoleName(items []*rolepkg.Role, id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	for _, it := range items {
		if it.ID == id {
			return it.Name
		}
	}
	return ""
}

func roleValue(r org.Role, id uuid.UUID) string {
	if id != uuid.Nil {
		return id.String()
	}
	return string(r)
}

func parseRoleValue(v string) (org.Role, uuid.UUID) {
	if id, err := uuid.Parse(v); err == nil {
		return org.RoleCustom, id
	}
	return org.Role(v), uuid.Nil
}

// Register wires all org routes onto the mux.
func (h *OrgHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs", h.GetList)
	mux.HandleFunc("GET /orgs/new", h.GetNew)
	mux.HandleFunc("POST /orgs", h.PostCreate)
	mux.HandleFunc("GET /orgs/{org}/members", h.GetShow)
	mux.HandleFunc("POST /orgs/{org}/rename", h.PostRename)
	mux.HandleFunc("POST /orgs/{org}/members/{user}/remove", h.PostRemoveMember)
	mux.HandleFunc("POST /orgs/{org}/members/{user}/role", h.PostSetRole)
	mux.HandleFunc("POST /orgs/{org}/members/{user}/projects", h.PostSetAccess)
}
