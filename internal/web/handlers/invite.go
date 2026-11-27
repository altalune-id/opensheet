package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	rolepkg "altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// InviteHandler owns /orgs/{slug}/invites and /invites/accept.
type InviteHandler struct {
	Deps
	Invites *invite.Service
}

// NewInviteHandler wires the handler.
func NewInviteHandler(d Deps, orgs *org.Service, invites *invite.Service) *InviteHandler {
	d.Orgs = orgs
	return &InviteHandler{Deps: d, Invites: invites}
}

// GetList renders /orgs/{slug}/invites.
func (h *InviteHandler) GetList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	view := templates.InvitesView{OrgSlug: o.Slug, CanManage: holds(r, authz.MembersWrite), Disabled: !h.Caps.InvitesEnabled}
	if view.CanManage {
		items, err := h.Invites.ListPending(r.Context())
		if err != nil {
			h.LogErr("web invite: list", err)
			h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load invites.", err)
			return
		}
		custom := h.customRoles(r)
		view.Invites = inviteRows(items, custom)
		view.CustomRoles = grantableCustomRoles(r, custom)
	}
	Render(w, r, templates.InvitesLayout(h.LayoutForOrg(r, "Invites", o.Slug, "invites"), view))
}

// PostSend handles POST /orgs/{slug}/invites.
func (h *InviteHandler) PostSend(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	slug := o.Slug
	if !holds(r, authz.MembersWrite) {
		h.refuseRole(w, r, "invites.read_only")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	role, roleID := parseInviteRole(strings.TrimSpace(r.PostForm.Get("role")))
	if role == "" {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad role", "Role must be admin, member or a custom role.")
		return
	}
	if _, err := h.Invites.Send(r.Context(), invite.SendRequest{Email: email, Role: role, RoleID: roleID}); err != nil {
		h.LogErr("web invite: send", err)
		if authz.IsDeniedError(err) {
			h.refuseRole(w, r, "invites.read_only", err)
			return
		}
		if _, escalated := errors.AsType[*authz.EscalationError](err); escalated {
			h.refuseRole(w, r, "invites.error_escalation", err)
			return
		}
		if invite.IsInvitesDisabledError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Invites disabled", err.Error(), err)
			return
		}
		h.ErrorPage(w, r, http.StatusBadRequest, "Send failed", err.Error())
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+slug+"/invites"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// PostRevoke handles POST /orgs/{slug}/invites/{id}/revoke.
func (h *InviteHandler) PostRevoke(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	o, r := sc.org, sc.req
	slug := o.Slug
	if !holds(r, authz.MembersWrite) {
		h.refuseRole(w, r, "invites.read_only")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed invite id.")
		return
	}
	if err := h.Invites.Revoke(r.Context(), id); err != nil {
		h.LogErr("web invite: revoke", err)
		if authz.IsDeniedError(err) {
			h.refuseRole(w, r, "invites.read_only", err)
			return
		}
		if invite.IsNotFoundError(err) {
			h.ErrorPage(w, r, http.StatusNotFound, "Invite not found", "That invite no longer exists.", err)
			return
		}
		// SECURITY: err.Error() names internal ids, so it stays in the log and never reaches the page.
		h.ErrorPage(w, r, http.StatusInternalServerError, "Revoke failed", "Could not revoke that invite.")
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+slug+"/invites"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// GetAccept handles GET /invites/accept?token=...
func (h *InviteHandler) GetAccept(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		h.ErrorPage(w, r, http.StatusBadRequest, "Missing token", "The invite link is malformed.")
		return
	}
	p, sid, authed := h.LoadSession(r)
	if !authed {
		web.SetCookie(w, web.CookieOpts{
			Name:         web.InviteCookieName,
			Value:        web.SignCookie(h.Key(web.PurposeInvite), token),
			BasePath:     h.Cfg.HTTP.BasePath,
			CookieSecure: h.SecureCookie(r),
			MaxAge:       int((15 * time.Minute).Seconds()),
		})
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	res, err := h.Invites.Accept(r.Context(), invite.AcceptRequest{
		Token: token,
		Email: p.Email,
		Name:  p.Name,
	})
	if err != nil {
		switch {
		case invite.IsNotFoundError(err), invite.IsExpiredError(err), invite.IsAlreadyUsedError(err):
			h.ErrorPage(w, r, http.StatusGone, "Invite not usable", err.Error())
			return
		case invite.IsTokenMismatchError(err):
			h.ErrorPage(w, r, http.StatusForbidden, "Invite mismatch", "This invite is for a different email.")
			return
		default:
			h.LogErr("web invite: accept", err)
			// SECURITY: err.Error() names internal ids, so the page shows the code and request id instead.
			h.ErrorPage(w, r, http.StatusInternalServerError, "Accept failed", "Could not accept that invite.", err)
			return
		}
	}
	o, err := h.Orgs.ByID(r.Context(), res.Invite.OrgID)
	if err != nil {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs"), http.StatusSeeOther)
		return
	}
	web.ClearCookie(w, web.InviteCookieName, h.Cfg.HTTP.BasePath, h.SecureCookie(r))
	// Rehydrate the session with the just-joined org so the dashboard renders the new tenant immediately — the pre-invite Principal has ActiveOrgID = uuid.Nil.
	p.ActiveOrgID = o.ID
	if id := h.firstProject(r.Context(), p, o.ID); id != uuid.Nil {
		p.ActiveProjectID = id
	}
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("web invite: refresh session", err)
	}
	dest := "/orgs/" + o.Slug
	if h.Cfg.Compliance.RequireAcceptance && p.TermsAcceptedAt.IsZero() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/welcome")+"?return_to="+url.QueryEscape(dest), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, dest), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

func (h *InviteHandler) firstProject(ctx context.Context, p session.Principal, orgID uuid.UUID) uuid.UUID {
	if h.Projects == nil {
		return uuid.Nil
	}
	tctx, err := h.Authz.Bind(ctx, p, tenant.Context{OrgID: orgID, UserID: p.UserID})
	if err != nil {
		h.LogErr("web invite: bind", err)
		return uuid.Nil
	}
	projects, err := h.Projects.List(tctx, orgID)
	if err != nil || len(projects) == 0 {
		return uuid.Nil
	}
	return projects[0].ID
}

//i18n:use invites.error_escalation

func parseInviteRole(v string) (invite.Role, uuid.UUID) {
	if id, err := uuid.Parse(v); err == nil {
		return invite.RoleCustom, id
	}
	if r := invite.Role(v); r.IsValid() {
		return r, uuid.Nil
	}
	return "", uuid.Nil
}

func grantableCustomRoles(r *http.Request, items []*rolepkg.Role) []templates.RoleOption {
	held := authz.Held(r.Context())
	out := make([]templates.RoleOption, 0, len(items))
	for _, it := range items {
		if authz.Covers(held, it.Permissions...) {
			out = append(out, templates.RoleOption{Value: it.ID.String(), Label: it.Name})
		}
	}
	return out
}

func inviteRows(items []*invite.Invite, custom []*rolepkg.Role) []templates.InviteRow {
	out := make([]templates.InviteRow, 0, len(items))
	for _, i := range items {
		role := string(i.Role)
		if name := customRoleName(custom, i.RoleID); name != "" {
			role = name
		}
		out = append(out, templates.InviteRow{
			ID:        i.ID.String(),
			Email:     i.Email,
			Role:      role,
			ExpiresAt: i.ExpiresAt.Format(time.RFC3339),
		})
	}
	return out
}

// Register wires all invite routes onto the mux.
func (h *InviteHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/invites", h.GetList)
	mux.HandleFunc("POST /orgs/{org}/invites", h.PostSend)
	mux.HandleFunc("POST /orgs/{org}/invites/{id}/revoke", h.PostRevoke)
	mux.HandleFunc("GET /invites/accept", h.GetAccept)
}
