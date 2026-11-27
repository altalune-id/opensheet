package handlers

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// RoleLister lists the custom roles of the org on ctx, for the role pickers.
type RoleLister interface {
	List(ctx context.Context) ([]*role.Role, error)
}

// RoleService is the slice of the custom-role service the console uses.
type RoleService interface {
	RoleLister
	Allowed(ctx context.Context) (bool, error)
	HeldBy(ctx context.Context) (map[uuid.UUID]int, error)
	Get(ctx context.Context, id uuid.UUID) (*role.Role, error)
	Create(ctx context.Context, name, description string, perms []authz.Permission) (*role.Role, error)
	Update(ctx context.Context, id uuid.UUID, name, description string, perms []authz.Permission, ifVersion int64) (*role.Role, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// RoleHandler owns the /orgs/{org}/roles routes.
type RoleHandler struct {
	Deps
	roles RoleService
}

// NewRoleHandler wires the handler.
func NewRoleHandler(d Deps, roles RoleService) *RoleHandler {
	return &RoleHandler{Deps: d, roles: roles}
}

//i18n:use roles.read_only
//i18n:use roles.managers_only
//i18n:use roles.error_escalation

// GetList renders /orgs/{org}/roles.
func (h *RoleHandler) GetList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	h.renderList(w, sc, http.StatusOK, "", "")
}

func (h *RoleHandler) renderList(w http.ResponseWriter, sc OrgScope, status int, errMsg, errCode string) {
	o, r := sc.org, sc.req
	if !holds(r, authz.MembersWrite) {
		h.refuseRole(w, r, "roles.managers_only")
		return
	}
	allowed, err := h.roles.Allowed(r.Context())
	if err != nil {
		h.LogErr("web role: allowed", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load roles.", err)
		return
	}
	items, err := h.roles.List(r.Context())
	if err != nil {
		h.LogErr("web role: list", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load roles.", err)
		return
	}
	held, err := h.roles.HeldBy(r.Context())
	if err != nil {
		h.LogErr("web role: held by", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load roles.", err)
		return
	}
	v := templates.RolesView{OrgSlug: o.Slug, StaffOrg: !allowed, CanEdit: allowed && holds(r, authz.RolesWrite), Error: errMsg, ErrorCode: errCode}
	for _, name := range []string{"owner", "admin", "member"} {
		v.Builtins = append(v.Builtins, templates.BuiltinRoleRow{Name: name, Perms: len(authz.Builtin(name))})
	}
	for _, it := range items {
		v.Roles = append(v.Roles, templates.RoleRow{
			ID: it.ID.String(), Name: it.Name, Description: it.Description, Perms: len(it.Permissions), Held: held[it.ID] > 0,
		})
	}
	tr := i18n.TranslatorFrom(r.Context())
	RenderStatus(w, r, status, templates.RolesLayout(h.LayoutForOrg(r, tr.T("roles.title"), o.Slug, "roles"), v))
}

// GetNew renders /orgs/{org}/roles/new.
func (h *RoleHandler) GetNew(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireRolesWrite(w, r)
	if !ok {
		return
	}
	v := templates.RoleEditView{OrgSlug: sc.org.Slug}
	h.applyCopy(sc.req, &v, nil)
	h.renderEdit(w, sc, http.StatusOK, v)
}

// GetEdit renders /orgs/{org}/roles/{id}.
func (h *RoleHandler) GetEdit(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireRolesWrite(w, r)
	if !ok {
		return
	}
	id, ok := h.roleID(w, sc.req)
	if !ok {
		return
	}
	it, err := h.roles.Get(sc.req.Context(), id)
	if err != nil {
		h.refuseSave(w, sc, templates.RoleEditView{OrgSlug: sc.org.Slug}, err)
		return
	}
	v := templates.RoleEditView{OrgSlug: sc.org.Slug, ID: it.ID.String(), Name: it.Name, Description: it.Description, Version: it.Version}
	h.applyCopy(sc.req, &v, it.Permissions)
	h.renderEdit(w, sc, http.StatusOK, v)
}

// PostCreate handles POST /orgs/{org}/roles.
func (h *RoleHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireRolesWrite(w, r)
	if !ok {
		return
	}
	v, perms, ok := h.parseForm(w, sc)
	if !ok {
		return
	}
	if _, err := h.roles.Create(sc.req.Context(), v.Name, v.Description, perms); err != nil {
		h.refuseSave(w, sc, v, err)
		return
	}
	h.redirectList(w, sc)
}

// PostUpdate handles POST /orgs/{org}/roles/{id}.
func (h *RoleHandler) PostUpdate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireRolesWrite(w, r)
	if !ok {
		return
	}
	id, ok := h.roleID(w, sc.req)
	if !ok {
		return
	}
	v, perms, ok := h.parseForm(w, sc)
	if !ok {
		return
	}
	v.ID = id.String()
	if _, err := h.roles.Update(sc.req.Context(), id, v.Name, v.Description, perms, v.Version); err != nil {
		h.refuseSave(w, sc, v, err)
		return
	}
	h.redirectList(w, sc)
}

// PostDelete handles POST /orgs/{org}/roles/{id}/delete.
func (h *RoleHandler) PostDelete(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireRolesWrite(w, r)
	if !ok {
		return
	}
	id, ok := h.roleID(w, sc.req)
	if !ok {
		return
	}
	err := h.roles.Delete(sc.req.Context(), id)
	if err == nil {
		h.redirectList(w, sc)
		return
	}
	if authz.IsDeniedError(err) {
		h.refuseRole(w, sc.req, "roles.read_only", err)
		return
	}
	status, msg, known := roleRefusal(sc.req, err)
	if !known {
		h.LogErr("web role: delete", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Delete failed", "Could not delete that role.")
		return
	}
	h.renderList(w, sc, status, msg, ErrorRef(err))
}

func (h *RoleHandler) requireRolesWrite(w http.ResponseWriter, r *http.Request) (OrgScope, bool) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return OrgScope{}, false
	}
	if !holds(sc.req, authz.RolesWrite) {
		h.refuseRole(w, sc.req, "roles.read_only")
		return OrgScope{}, false
	}
	return sc, true
}

func (h *RoleHandler) roleID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusNotFound, "Not found", "That role does not exist.")
		return uuid.Nil, false
	}
	return id, true
}

func (h *RoleHandler) parseForm(w http.ResponseWriter, sc OrgScope) (templates.RoleEditView, []authz.Permission, bool) {
	r := sc.req
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return templates.RoleEditView{}, nil, false
	}
	version, _ := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	perms := make([]authz.Permission, 0, len(r.PostForm["perm"]))
	for _, p := range r.PostForm["perm"] {
		perms = append(perms, authz.Permission(p))
	}
	v := templates.RoleEditView{
		OrgSlug: sc.org.Slug, Version: version,
		Name: strings.TrimSpace(r.PostForm.Get("name")), Description: strings.TrimSpace(r.PostForm.Get("description")),
		Perms: permOptions(r, perms),
	}
	return v, perms, true
}

func (h *RoleHandler) applyCopy(r *http.Request, v *templates.RoleEditView, current []authz.Permission) {
	q := r.URL.Query()
	from := q.Get("from")
	if from == "" {
		v.Perms = permOptions(r, current)
		return
	}
	v.Name = strings.TrimSpace(q.Get("name"))
	v.Description = strings.TrimSpace(q.Get("description"))
	if n, err := strconv.ParseInt(q.Get("version"), 10, 64); err == nil {
		v.Version = n
	}
	v.Perms = permOptions(r, authz.Builtin(from))
}

func permOptions(r *http.Request, checked []authz.Permission) []templates.PermOption {
	tr := i18n.TranslatorFrom(r.Context())
	defs := authz.Catalogue()
	out := make([]templates.PermOption, 0, len(defs))
	for _, d := range defs {
		out = append(out, templates.PermOption{Value: string(d.Permission), Label: tr.T(d.LabelKey), Checked: slices.Contains(checked, d.Permission)})
	}
	return out
}

func (h *RoleHandler) renderEdit(w http.ResponseWriter, sc OrgScope, status int, v templates.RoleEditView) {
	tr := i18n.TranslatorFrom(sc.req.Context())
	title := tr.T("role_edit.title_new")
	if v.ID != "" {
		title = tr.T("roles.title")
	}
	RenderStatus(w, sc.req, status, templates.RoleEditLayout(h.LayoutForOrg(sc.req, title, sc.org.Slug, "roles"), v))
}

func (h *RoleHandler) refuseSave(w http.ResponseWriter, sc OrgScope, v templates.RoleEditView, err error) {
	r := sc.req
	if authz.IsDeniedError(err) {
		h.refuseRole(w, r, "roles.read_only", err)
		return
	}
	if authz.IsEscalationError(err) {
		h.refuseRole(w, r, "roles.error_escalation", err)
		return
	}
	if role.IsNotFoundError(err) {
		h.ErrorPage(w, r, http.StatusNotFound, "Not found", i18n.TranslatorFrom(r.Context()).T("roles.error_not_found"), err)
		return
	}
	status, msg, known := roleRefusal(r, err)
	if !known {
		h.LogErr("web role: save", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "Save failed", "Could not save that role.")
		return
	}
	v.Error, v.ErrorCode, v.Stale = msg, ErrorRef(err), role.IsStaleVersionError(err)
	h.renderEdit(w, sc, status, v)
}

//i18n:use roles.error_system_org
//i18n:use roles.error_in_use
//i18n:use roles.error_not_found
//i18n:use role_edit.error_invalid_name
//i18n:use role_edit.error_already_exists
//i18n:use role_edit.error_invalid_permission
//i18n:use role_edit.error_stale
func roleRefusal(r *http.Request, err error) (status int, msg string, ok bool) { //nolint:nonamedreturns // status and message differ in role
	tr := i18n.TranslatorFrom(r.Context())
	switch {
	case role.IsInvalidNameError(err):
		return http.StatusUnprocessableEntity, tr.T("role_edit.error_invalid_name"), true
	case role.IsAlreadyExistsError(err):
		return http.StatusConflict, tr.T("role_edit.error_already_exists"), true
	case role.IsInvalidPermissionError(err):
		return http.StatusUnprocessableEntity, tr.T("role_edit.error_invalid_permission"), true
	case role.IsStaleVersionError(err):
		return http.StatusConflict, tr.T("role_edit.error_stale"), true
	case role.IsSystemOrgError(err):
		return http.StatusConflict, tr.T("roles.error_system_org"), true
	case role.IsInUseError(err):
		return http.StatusConflict, tr.T("roles.error_in_use"), true
	case role.IsNotFoundError(err):
		return http.StatusNotFound, tr.T("roles.error_not_found"), true
	}
	return 0, "", false
}

func (h *RoleHandler) redirectList(w http.ResponseWriter, sc OrgScope) {
	http.Redirect(w, sc.req, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+sc.org.Slug+"/roles"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// Register wires the role routes onto the mux.
func (h *RoleHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/roles", h.GetList)
	mux.HandleFunc("GET /orgs/{org}/roles/new", h.GetNew)
	mux.HandleFunc("POST /orgs/{org}/roles", h.PostCreate)
	mux.HandleFunc("GET /orgs/{org}/roles/{id}", h.GetEdit)
	mux.HandleFunc("POST /orgs/{org}/roles/{id}", h.PostUpdate)
	mux.HandleFunc("POST /orgs/{org}/roles/{id}/delete", h.PostDelete)
}
